// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"
)

// loadedCR is the minimum projection of an IOSXEConfig this tool
// needs. We avoid importing the full api/config/v1alpha1 decode path
// so the lint tool stays light — multi-doc YAML and partial-shape
// tolerance matter more here than full-round-trip fidelity.
type loadedCR struct {
	// FullName is "<namespace>/<name>" for identification in
	// claimer lists and error messages.
	FullName string

	// DeviceName is spec.deviceRef.name.
	DeviceName string

	// ManagedFamilies is the closed set of families the CR claims.
	ManagedFamilies []string

	// InlineSource is spec.source.inline, decoded to a netascode body.
	// When spec.source.configMapRef is used instead, InlineSource is
	// nil — the tool reports that families are claimed but skips the
	// drift computation (it has no access to the ConfigMap content
	// from a file-only loader).
	InlineSource map[string]any

	// SourceViaConfigMap captures the ConfigMap name/key when the CR
	// uses configMapRef. Used in reports so the operator sees why a
	// family was claimed but not drift-checked.
	SourceViaConfigMap string
}

// discoverCRFiles walks the supplied paths (files and/or directories)
// and returns every .yaml / .yml file. Non-YAML files are skipped so
// a "kubectl apply -f ." workflow that includes README.md stays
// valid.
func discoverCRFiles(paths []string) ([]string, error) {
	seen := map[string]struct{}{}
	var out []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, fmt.Errorf("stat %q: %w", p, err)
		}
		if !info.IsDir() {
			if _, dup := seen[p]; !dup {
				out = append(out, p)
				seen[p] = struct{}{}
			}
			continue
		}
		err = filepath.WalkDir(p, func(sub string, d fs.DirEntry, werr error) error {
			if werr != nil {
				return werr
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(sub))
			if ext != ".yaml" && ext != ".yml" {
				return nil
			}
			if _, dup := seen[sub]; !dup {
				out = append(out, sub)
				seen[sub] = struct{}{}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// loadCRsFromFiles reads every discovered YAML file and returns the
// IOSXEConfig CRs whose spec.deviceRef.name matches deviceName.
// Non-IOSXEConfig documents (scope objects, unrelated Kubernetes
// resources) are silently skipped — this tool's job is drift
// reporting, not YAML validation.
func loadCRsFromFiles(paths []string, deviceName string) ([]loadedCR, error) {
	var out []loadedCR
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, fmt.Errorf("read %q: %w", p, err)
		}
		for i, doc := range splitYAMLDocs(raw) {
			if len(strings.TrimSpace(string(doc))) == 0 {
				continue
			}
			cr, isCR, err := tryDecodeIOSXEConfig(doc)
			if err != nil {
				return nil, fmt.Errorf("%s doc #%d: %w", p, i, err)
			}
			if !isCR {
				continue
			}
			if cr.DeviceName != deviceName {
				continue
			}
			out = append(out, cr)
		}
	}
	return out, nil
}

// docShape is the minimum unmarshal target that identifies an
// IOSXEConfig. Same philosophy as the old lint's docShape: decode
// just enough to route.
type docShape struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		DeviceRef struct {
			Name string `json:"name"`
		} `json:"deviceRef"`
		ManagedFamilies []string `json:"managedFamilies"`
		Source          struct {
			Inline       map[string]any `json:"inline,omitempty"`
			ConfigMapRef *struct {
				Name string `json:"name"`
				Key  string `json:"key"`
			} `json:"configMapRef,omitempty"`
		} `json:"source"`
	} `json:"spec"`
}

func tryDecodeIOSXEConfig(doc []byte) (loadedCR, bool, error) {
	var d docShape
	if err := yaml.Unmarshal(doc, &d); err != nil {
		return loadedCR{}, false, fmt.Errorf("parse YAML: %w", err)
	}
	if d.Kind != "IOSXEConfig" {
		return loadedCR{}, false, nil
	}
	if d.APIVersion != "config.cisco.vk/v1alpha1" {
		// Present but wrong version: treat as not-an-IOSXEConfig
		// rather than error. The operator may have a future-version
		// CR the current tool doesn't understand.
		return loadedCR{}, false, nil
	}
	cr := loadedCR{
		FullName:        d.Metadata.Namespace + "/" + d.Metadata.Name,
		DeviceName:      d.Spec.DeviceRef.Name,
		ManagedFamilies: append([]string(nil), d.Spec.ManagedFamilies...),
		InlineSource:    d.Spec.Source.Inline,
	}
	if d.Spec.Source.ConfigMapRef != nil && d.Spec.Source.ConfigMapRef.Name != "" {
		cr.SourceViaConfigMap = d.Spec.Source.ConfigMapRef.Name + "[" + d.Spec.Source.ConfigMapRef.Key + "]"
	}
	return cr, true, nil
}

// splitYAMLDocs performs a minimal "\n---" split. Matches the old
// lint tool's behaviour so pre-existing operator workflows keep
// working.
func splitYAMLDocs(raw []byte) [][]byte {
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	parts := strings.Split(s, "\n---")
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimPrefix(p, "---")
		out = append(out, []byte(p))
	}
	return out
}

// buildDriftInputs folds the loaded CRs into the per-family work
// unit computeReport consumes. Families inherit all claimers; when
// multiple CRs declare the same family, the last-loaded CR's intent
// wins on overlap — operators who care about ordering here are
// encouraged to use the engine's Lease arbitration in-cluster rather
// than rely on lint-time merge semantics.
func buildDriftInputs(deviceName string, crs []loadedCR) driftInputs {
	out := driftInputs{
		device:   deviceName,
		claimers: map[string][]string{},
		intents:  map[string]any{},
	}
	for _, cr := range crs {
		for _, fam := range cr.ManagedFamilies {
			out.claimers[fam] = append(out.claimers[fam], cr.FullName)
			if body, ok := cr.InlineSource[fam]; ok {
				out.intents[fam] = body
			}
		}
	}
	return out
}
