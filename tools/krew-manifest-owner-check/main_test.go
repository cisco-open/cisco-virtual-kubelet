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

const validContractManifest = `apiVersion: krew.googlecontainertools.github.com/v1alpha2
kind: Plugin
metadata:
  name: cisco-vk
spec:
  version: "v2026.9.0"
  homepage: https://github.com/cisco-open/cisco-virtual-kubelet
  shortDescription: Test manifest
  platforms:
  - selector:
      matchLabels:
        os: darwin
        arch: amd64
    uri: https://github.com/cisco-open/cisco-virtual-kubelet/releases/download/v2026.9.0/kubectl-ciscovk_v2026.9.0_darwin_amd64.tar.gz
    sha256: "1111111111111111111111111111111111111111111111111111111111111111"
    bin: kubectl-ciscovk
  - selector:
      matchLabels:
        os: darwin
        arch: arm64
    uri: https://github.com/cisco-open/cisco-virtual-kubelet/releases/download/v2026.9.0/kubectl-ciscovk_v2026.9.0_darwin_arm64.tar.gz
    sha256: "2222222222222222222222222222222222222222222222222222222222222222"
    bin: kubectl-ciscovk
  - selector:
      matchLabels:
        os: linux
        arch: amd64
    uri: https://github.com/cisco-open/cisco-virtual-kubelet/releases/download/v2026.9.0/kubectl-ciscovk_v2026.9.0_linux_amd64.tar.gz
    sha256: "3333333333333333333333333333333333333333333333333333333333333333"
    bin: kubectl-ciscovk
  - selector:
      matchLabels:
        os: linux
        arch: arm64
    uri: https://github.com/cisco-open/cisco-virtual-kubelet/releases/download/v2026.9.0/kubectl-ciscovk_v2026.9.0_linux_arm64.tar.gz
    sha256: "4444444444444444444444444444444444444444444444444444444444444444"
    bin: kubectl-ciscovk
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
		"dot segments": strings.Replace(
			validManifest,
			"releases/download/v2026.9.0/plugin.tar.gz",
			"releases/download/../../../../kubernetes-sigs/krew-index/releases/download/v1/plugin.tar.gz",
			1,
		),
		"encoded dot segments": strings.Replace(
			validManifest,
			"releases/download/v2026.9.0/plugin.tar.gz",
			"releases/download/%2e%2e/%2e%2e/other/project/plugin.tar.gz",
			1,
		),
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

func TestCompareManifestContractsAcceptsExactAndReorderedPlatforms(t *testing.T) {
	if err := compareManifestContracts([]byte(validContractManifest), []byte(validContractManifest)); err != nil {
		t.Fatalf("exact manifest contract rejected: %v", err)
	}
	const separator = "  - selector:"
	parts := strings.Split(validContractManifest, separator)
	if len(parts) != 5 {
		t.Fatalf("unexpected manifest fixture split: %d parts", len(parts))
	}
	reordered := parts[0]
	for _, index := range []int{4, 2, 1, 3} {
		reordered += separator + parts[index]
	}
	if err := compareManifestContracts([]byte(reordered), []byte(validContractManifest)); err != nil {
		t.Fatalf("platform-order-only change rejected: %v", err)
	}
}

func TestCompareManifestContractsRejectsDistributionDrift(t *testing.T) {
	parts := strings.Split(validContractManifest, "  - selector:")
	tests := map[string]string{
		"version": strings.Replace(validContractManifest, "v2026.9.0", "v2026.9.1", 1),
		"uri": strings.Replace(
			validContractManifest,
			"kubectl-ciscovk_v2026.9.0_darwin_amd64.tar.gz",
			"kubectl-ciscovk_v2026.9.0_darwin_other.tar.gz",
			1,
		),
		"sha256": strings.Replace(
			validContractManifest,
			strings.Repeat("1", 64),
			strings.Repeat("a", 64),
			1,
		),
		"bin":              strings.Replace(validContractManifest, "bin: kubectl-ciscovk", "bin: other", 1),
		"selector":         strings.Replace(validContractManifest, "arch: amd64", "arch: arm64", 1),
		"missing platform": strings.Join(parts[:4], "  - selector:"),
		"extra platform":   validContractManifest + "  - selector:" + parts[4],
		"extra field": strings.Replace(
			validContractManifest,
			"    bin: kubectl-ciscovk",
			"    bin: kubectl-ciscovk\n    files: []",
			1,
		),
		"invalid hash": strings.Replace(
			validContractManifest,
			strings.Repeat("1", 64),
			strings.Repeat("A", 64),
			1,
		),
		"duplicate key": strings.Replace(
			validContractManifest,
			"  version: \"v2026.9.0\"",
			"  version: \"v2026.9.0\"\n  version: \"v2026.9.0\"",
			1,
		),
	}
	for name, manifest := range tests {
		t.Run(name, func(t *testing.T) {
			if err := compareManifestContracts([]byte(manifest), []byte(validContractManifest)); err == nil {
				t.Fatal("drifted manifest contract accepted")
			}
		})
	}
}
