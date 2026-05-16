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
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func TestReportClassifiesSupportedAndUnsupportedFamilies(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--family-index", familyIndexPath(),
		filepath.Join("testdata", "device.nac.yaml"),
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"supported:",
		"  - snmp_server",
		"  - system",
		"  - vlan",
		"unsupported_passthrough:",
		"  - bespoke_feature",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("report missing %q:\n%s", want, out)
		}
	}
}

func TestEmitCRPopulatesManagedFamilies(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"emit-cr",
		"--family-index", familyIndexPath(),
		"--name", "edge-01",
		"--namespace", "network",
		filepath.Join("testdata", "device.nac.yaml"),
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"apiVersion: config.cisco.vk/v1alpha1",
		"kind: IOSXEConfig",
		"name: edge-01",
		"managedFamilies:",
		"- snmp_server",
		"- system",
		"- vlan",
		"bespoke_feature:",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("CR missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "- bespoke_feature") {
		t.Fatalf("unsupported family should remain in source.inline, not managedFamilies:\n%s", out)
	}
}

func TestMatrixRendersFamilyRows(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"matrix",
		"--family-index", familyIndexPath(),
		"--output", "-",
	}, nil, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("run exit=%d stderr=%s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"# CVK netascode family parity",
		"| `system` | managed | singleton | - | - | `/Cisco-IOS-XE-native:native` |",
		"| `vlan` | managed | keyed_list | `id` | - | `/Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list` |",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("matrix missing %q:\n%s", want, out)
		}
	}
}

func familyIndexPath() string {
	return filepath.Join("..", "..", "internal", "drivers", "iosxe", "configdriver", "schema", "families.yaml")
}
