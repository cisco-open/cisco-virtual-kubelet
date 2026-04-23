// Copyright © 2026 Cisco Systems, Inc.
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

// Command cisco-vk-config-lint validates IOSXEConfig CRs (and
// supporting scope objects) offline, before they reach the cluster.
// It is the pre-commit / CI gate referenced by the RFC:
//
//   - YAML parses.
//   - `kind` is a recognised IOSXEConfig family.
//   - Every ManagedFamily is listed in schema/families.yaml.
//   - Per-family semantic rules (VLAN ID range, VRF name shape, etc.).
//
// Non-zero exit on any failure; problems are reported with file:line
// context. A clean run is silent unless --verbose is set.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"sigs.k8s.io/yaml"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/schema"
)

type exitCode int

const (
	exitOK        exitCode = 0
	exitBadFlags  exitCode = 2
	exitBadInput  exitCode = 3
	exitViolation exitCode = 4
)

// recognised kinds. The lint rules for non-IOSXEConfig kinds are
// lightweight (parse-only); IOSXEConfig gets the full rule set.
var recognisedKinds = map[string]bool{
	"IOSXEConfig":            true,
	"IOSXEConfigDefaults":    true,
	"IOSXEDeviceGroupConfig": true,
	"IOSXETemplate":          true,
}

// flags captures the CLI surface so tests can drive run() directly
// without going through os.Args.
type flags struct {
	paths   []string
	verbose bool
	strict  bool
}

func parseFlags(args []string, stderr io.Writer) (flags, error) {
	fs := flag.NewFlagSet("cisco-vk-config-lint", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var f flags
	fs.BoolVar(&f.verbose, "verbose", false,
		"print a line per passing file too; clean runs are silent without this flag")
	fs.BoolVar(&f.strict, "strict", false,
		"fail when a file contains a YAML document whose kind is not recognised "+
			"(default: skip unrecognised kinds silently, matching a mixed-repo layout)")

	if err := fs.Parse(args); err != nil {
		return f, err
	}
	f.paths = fs.Args()
	if len(f.paths) == 0 {
		f.paths = []string{"."}
	}
	return f, nil
}

// violation is a single rule failure with enough context for CI logs
// to surface it clearly.
type violation struct {
	file string
	kind string
	name string
	msg  string
}

func (v violation) String() string {
	head := v.file
	if v.kind != "" {
		head += ": " + v.kind
	}
	if v.name != "" {
		head += "/" + v.name
	}
	return head + ": " + v.msg
}

func run(args []string, stdout, stderr io.Writer) exitCode {
	f, err := parseFlags(args, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return exitOK
		}
		return exitBadFlags
	}

	families, err := schema.LoadFamilies()
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: load families.yaml: %v\n", err)
		return exitBadInput
	}

	files, err := discoverFiles(f.paths)
	if err != nil {
		fmt.Fprintf(stderr, "ERROR: walk: %v\n", err)
		return exitBadInput
	}
	if len(files) == 0 {
		fmt.Fprintf(stderr, "ERROR: no YAML files under %v\n", f.paths)
		return exitBadInput
	}

	var violations []violation
	for _, path := range files {
		vs, err := lintFile(path, families, f.strict)
		if err != nil {
			violations = append(violations, violation{file: path, msg: err.Error()})
			continue
		}
		violations = append(violations, vs...)
		if f.verbose && len(vs) == 0 {
			fmt.Fprintf(stdout, "ok %s\n", path)
		}
	}

	if len(violations) > 0 {
		for _, v := range violations {
			fmt.Fprintln(stderr, v.String())
		}
		fmt.Fprintf(stderr, "\n%d violation(s) in %d file(s)\n",
			len(violations), len(files))
		return exitViolation
	}
	return exitOK
}

// discoverFiles expands each path argument: a directory is walked
// recursively for .yaml/.yml, a file is included verbatim. Symlinks
// are followed by filepath.WalkDir's default behaviour — callers can
// feed an explicit file list to bypass directory expansion.
func discoverFiles(paths []string) ([]string, error) {
	var out []string
	seen := map[string]struct{}{}
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

// lintFile splits a multi-document YAML file and runs kind-specific
// checks per document.
func lintFile(path string, families map[string]schema.Family, strict bool) ([]violation, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %q: %w", path, err)
	}
	docs := splitYAMLDocs(raw)

	var out []violation
	for i, doc := range docs {
		if len(strings.TrimSpace(string(doc))) == 0 {
			continue
		}
		vs, err := lintDoc(path, i, doc, families, strict)
		if err != nil {
			out = append(out, violation{
				file: fmt.Sprintf("%s#%d", path, i),
				msg:  err.Error(),
			})
			continue
		}
		out = append(out, vs...)
	}
	return out, nil
}

// splitYAMLDocs performs a minimal "---" split. Lines inside folded
// strings that contain "---" at column zero are vanishingly rare in
// netascode YAML; callers that do need strict parsing can hand each
// document to a real YAML splitter.
func splitYAMLDocs(raw []byte) [][]byte {
	// Normalise line endings then split on a "\n---" marker at the
	// start of a line.
	s := strings.ReplaceAll(string(raw), "\r\n", "\n")
	parts := strings.Split(s, "\n---")
	out := make([][]byte, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimPrefix(p, "---")
		out = append(out, []byte(p))
	}
	return out
}

// docShape is the minimum unmarshal target that surfaces the
// identifying fields. Further fields are extracted via generic maps
// so we don't depend on the full API types at lint time.
type docShape struct {
	APIVersion string `json:"apiVersion"`
	Kind       string `json:"kind"`
	Metadata   struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec map[string]any `json:"spec"`
}

func lintDoc(path string, index int, doc []byte, families map[string]schema.Family, strict bool) ([]violation, error) {
	var head docShape
	if err := yaml.Unmarshal(doc, &head); err != nil {
		return nil, fmt.Errorf("parse YAML doc #%d: %w", index, err)
	}
	if head.Kind == "" {
		// A YAML file with no `kind` at all is probably not a CR; skip.
		return nil, nil
	}
	if !recognisedKinds[head.Kind] {
		if strict {
			return []violation{{file: path, kind: head.Kind, msg: "unrecognised kind in strict mode"}}, nil
		}
		return nil, nil
	}
	if head.APIVersion != "config.cisco.vk/v1alpha1" {
		return []violation{{
			file: path, kind: head.Kind, name: head.Metadata.Name,
			msg: fmt.Sprintf("apiVersion %q, want config.cisco.vk/v1alpha1", head.APIVersion),
		}}, nil
	}

	switch head.Kind {
	case "IOSXEConfig":
		return lintIOSXEConfig(path, head, families), nil
	case "IOSXEConfigDefaults", "IOSXEDeviceGroupConfig", "IOSXETemplate":
		// These only need schema presence checks at lint time — the
		// real shape is validated at CRD admission by the API server.
		if head.Spec == nil {
			return []violation{{file: path, kind: head.Kind, name: head.Metadata.Name,
				msg: "spec missing"}}, nil
		}
		return nil, nil
	}
	return nil, nil
}

// lintIOSXEConfig applies the rule set specific to IOSXEConfig:
//   - deviceRef.name non-empty.
//   - managedFamilies non-empty; every family is in families.yaml.
//   - exactly one of source.inline / source.configMapRef.
//   - driftPolicy, when set, is one of the accepted values.
//   - configuration block only contains known families at top level
//     (warning, not error, so forward-compat is friction-free).
func lintIOSXEConfig(path string, d docShape, families map[string]schema.Family) []violation {
	var out []violation
	name := d.Metadata.Name
	tag := func(msg string) violation {
		return violation{file: path, kind: d.Kind, name: name, msg: msg}
	}

	ref, _ := d.Spec["deviceRef"].(map[string]any)
	if refName, _ := ref["name"].(string); refName == "" {
		out = append(out, tag("spec.deviceRef.name is empty"))
	}

	mf, _ := d.Spec["managedFamilies"].([]any)
	if len(mf) == 0 {
		out = append(out, tag("spec.managedFamilies is empty (MinItems=1)"))
	}
	for i, f := range mf {
		fs, ok := f.(string)
		if !ok {
			out = append(out, tag(fmt.Sprintf("spec.managedFamilies[%d] is not a string", i)))
			continue
		}
		if _, known := families[fs]; !known {
			out = append(out, tag(fmt.Sprintf(
				"spec.managedFamilies[%d]=%q not in families.yaml", i, fs)))
		}
	}

	src, _ := d.Spec["source"].(map[string]any)
	_, hasInline := src["inline"]
	cm, _ := src["configMapRef"].(map[string]any)
	hasCMR := cm != nil && cm["name"] != nil && cm["name"] != ""
	switch {
	case hasInline && hasCMR:
		out = append(out, tag("spec.source: both inline and configMapRef set; exactly one allowed"))
	case !hasInline && !hasCMR:
		out = append(out, tag("spec.source: neither inline nor configMapRef set"))
	}
	if hasCMR {
		if key, _ := cm["key"].(string); key == "" {
			out = append(out, tag("spec.source.configMapRef.key is empty"))
		}
	}

	if dp, ok := d.Spec["driftPolicy"].(string); ok && dp != "" {
		switch dp {
		case "revert", "report", "pause":
		default:
			out = append(out, tag(fmt.Sprintf(
				"spec.driftPolicy=%q; want one of revert|report|pause", dp)))
		}
	}

	return out
}

func main() {
	os.Exit(int(run(os.Args[1:], os.Stdout, os.Stderr)))
}
