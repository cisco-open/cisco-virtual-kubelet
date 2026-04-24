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

package schema

import (
	_ "embed"
	"fmt"

	"sigs.k8s.io/yaml"
)

//go:embed families.yaml
var familiesYAML []byte

//go:embed yang-versions.yaml
var yangVersionsYAML []byte

// Family describes one netascode family and how it is expressed in YANG.
// The shape mirrors families.yaml exactly; adding a field to the file
// requires adding it here in the same change so decoding fails loudly
// rather than silently dropping data.
type Family struct {
	// YANGPaths lists the Cisco-IOS-XE-native YANG xpaths the family
	// reads and writes. This is the default dialect; every family
	// has a value here.
	YANGPaths []string `json:"yang_paths"`

	// OpenConfigPaths optionally lists the OpenConfig YANG xpaths
	// that model the same family. When set, gNMI transports can
	// switch to OpenConfig dialect by family (Phase 6 follow-up
	// per-writer migration), and external tooling
	// (`cisco-vk-config-docs`, the lint tool) can show a multi-
	// vendor target. Empty when no clean OpenConfig analog exists
	// (Cisco-IA RPCs, vendor-specific knobs).
	OpenConfigPaths []string `json:"openconfig_paths,omitempty"`

	Shape     string   `json:"shape"`
	KeyFields []string `json:"key_fields,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
	Portal    string   `json:"portal,omitempty"`
}

// PathsForDialect returns the YANG xpaths for the family, falling
// back to native paths when the requested dialect has no entries.
// dialect is "native" or "openconfig"; anything else is treated as
// native. The fallback is intentional: a writer keyed off a family
// without OpenConfig coverage stays functional, just on the native
// path.
func (f Family) PathsForDialect(dialect string) []string {
	if dialect == "openconfig" && len(f.OpenConfigPaths) > 0 {
		return f.OpenConfigPaths
	}
	return f.YANGPaths
}

// YANGRelease describes one supported Cisco-IOS-XE YANG release.
type YANGRelease struct {
	Version string `json:"version"`
	Status  string `json:"status"`
	Default bool   `json:"default,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

type yangVersionsFile struct {
	Releases []YANGRelease `json:"releases"`
}

// LoadFamilies decodes families.yaml into a map keyed by family name.
// The embed guarantees the file is present at build time; a decode error
// means the file is malformed and is a programming error, not a runtime
// condition — callers should surface it as such.
func LoadFamilies() (map[string]Family, error) {
	var out map[string]Family
	if err := yaml.Unmarshal(familiesYAML, &out); err != nil {
		return nil, fmt.Errorf("parse families.yaml: %w", err)
	}
	return out, nil
}

// LoadYANGReleases decodes yang-versions.yaml.
func LoadYANGReleases() ([]YANGRelease, error) {
	var file yangVersionsFile
	if err := yaml.Unmarshal(yangVersionsYAML, &file); err != nil {
		return nil, fmt.Errorf("parse yang-versions.yaml: %w", err)
	}
	return file.Releases, nil
}

// DefaultYANGRelease returns the release marked default:true. If no such
// release is declared (misconfiguration), the first supported release is
// returned as a reasonable fallback.
func DefaultYANGRelease() (YANGRelease, error) {
	releases, err := LoadYANGReleases()
	if err != nil {
		return YANGRelease{}, err
	}
	for _, r := range releases {
		if r.Default {
			return r, nil
		}
	}
	for _, r := range releases {
		if r.Status == "supported" {
			return r, nil
		}
	}
	return YANGRelease{}, fmt.Errorf("no supported YANG release declared")
}
