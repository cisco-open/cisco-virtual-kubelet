// Copyright 2026 Cisco Systems Inc.
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
	"strings"
	"testing"
)

const validManifest = `apiVersion: krew.googlecontainertools.github.com/v1alpha2
kind: Plugin
metadata:
  name: cisco-vk
spec:
  homepage: https://github.com/cisco-open/cisco-virtual-kubelet
  platforms:
  - uri: https://github.com/cisco-open/cisco-virtual-kubelet/releases/download/v2026.9.0/plugin.tar.gz
`

func TestValidateManifestOwnership(t *testing.T) {
	if err := validateManifestOwnership([]byte(validManifest)); err != nil {
		t.Fatalf("valid manifest rejected: %v", err)
	}
}

func TestValidateManifestOwnershipRejectsForeignEntry(t *testing.T) {
	tests := map[string]string{
		"name":     strings.Replace(validManifest, "name: cisco-vk", "name: other", 1),
		"homepage": strings.Replace(validManifest, expectedHomepage, "https://example.com/other", 1),
		"archive":  strings.Replace(validManifest, "https://github.com/cisco-open/cisco-virtual-kubelet/releases/download/", "https://github.com/other/project/releases/download/", 1),
	}
	for name, manifest := range tests {
		t.Run(name, func(t *testing.T) {
			if err := validateManifestOwnership([]byte(manifest)); err == nil {
				t.Fatal("foreign manifest accepted")
			}
		})
	}
}

func TestValidateManifestOwnershipRejectsDuplicateKeys(t *testing.T) {
	manifest := strings.Replace(validManifest, "kind: Plugin", "kind: Plugin\nkind: Other", 1)
	if err := validateManifestOwnership([]byte(manifest)); err == nil {
		t.Fatal("manifest with duplicate keys accepted")
	}
}
