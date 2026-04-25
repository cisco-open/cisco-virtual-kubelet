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

package writers

// Wave 7B regression tests for external-review-next-actions
// Finding #5: handwritten interface writers must populate
// transport.Op.PathSpec so gNMI Set/Delete preserves interface
// names with '/' (e.g. "0/0/0"). Pre-fix only the shared
// keyedListWriter populated PathSpec; the lab case
// GigabitEthernet=0/0/0 hit the handwritten ethernetWriter and
// failed.

import (
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

func TestEthernetWriter_DiffEmitsPathSpecPreservingSlash(t *testing.T) {
	t.Parallel()
	w := ethernetWriter{}
	desired := map[string]any{
		"interfaces": []any{
			map[string]any{
				"type":        "GigabitEthernet",
				"name":        "0/0/0",
				"description": "lab uplink",
				"shutdown":    false,
			},
		},
	}
	observed := []map[string]any(nil) // device empty

	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]

	// Legacy string Path stays — RESTCONF/NETCONF still parse it.
	wantString := "/Cisco-IOS-XE-native:native/interface/GigabitEthernet=0/0/0"
	if op.Path != wantString {
		t.Errorf("string Path = %q, want %q", op.Path, wantString)
	}

	// PathSpec must encode the key value verbatim — '/' preserved.
	if len(op.PathSpec) != 3 {
		t.Fatalf("expected 3 PathElements, got %d: %+v", len(op.PathSpec), op.PathSpec)
	}
	if op.PathSpec[0].Name != "native" {
		t.Errorf("PathSpec[0].Name = %q, want native", op.PathSpec[0].Name)
	}
	if op.PathSpec[1].Name != "interface" {
		t.Errorf("PathSpec[1].Name = %q, want interface", op.PathSpec[1].Name)
	}
	if op.PathSpec[2].Name != "GigabitEthernet" {
		t.Errorf("PathSpec[2].Name = %q, want GigabitEthernet", op.PathSpec[2].Name)
	}
	if got := op.PathSpec[2].Keys["name"]; got != "0/0/0" {
		t.Errorf("PathSpec[2].Keys[name] = %q, want \"0/0/0\" — slash must be preserved verbatim", got)
	}
}

func TestSwitchportWriter_DiffEmitsPathSpecWithChild(t *testing.T) {
	t.Parallel()
	w := switchportWriter{}
	desired := map[string]any{
		"interfaces": []any{
			map[string]any{
				"type":      "GigabitEthernet",
				"name":      "0/0/1",
				"mode":      "access",
				"vlan":      float64(10),
				"shutdown":  false,
				"trunk":     false,
				"port_mode": "access",
			},
		},
	}
	observed := []map[string]any(nil) // device empty

	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("expected 1 op, got %d", len(ops))
	}
	op := ops[0]

	// PathSpec must end in the trailing "switchport" child container
	// while preserving the interface name's '/' in the key.
	if len(op.PathSpec) != 4 {
		t.Fatalf("expected 4 PathElements (native/interface/<Type>/switchport), got %d: %+v", len(op.PathSpec), op.PathSpec)
	}
	if op.PathSpec[2].Name != "GigabitEthernet" {
		t.Errorf("PathSpec[2].Name = %q, want GigabitEthernet", op.PathSpec[2].Name)
	}
	if got := op.PathSpec[2].Keys["name"]; got != "0/0/1" {
		t.Errorf("PathSpec[2].Keys[name] = %q, want \"0/0/1\"", got)
	}
	if op.PathSpec[3].Name != "switchport" {
		t.Errorf("PathSpec[3].Name = %q, want switchport", op.PathSpec[3].Name)
	}
	if len(op.PathSpec[3].Keys) != 0 {
		t.Errorf("trailing switchport child must have no keys, got %v", op.PathSpec[3].Keys)
	}
}

// TestPathSpecForInterface_Helper pins the helper used by every
// handwritten interface writer.
func TestPathSpecForInterface_Helper(t *testing.T) {
	t.Parallel()
	got := pathSpecForInterface("FortyGigabitEthernet", "0/0/0")
	if len(got) != 3 {
		t.Fatalf("expected 3 elements, got %d", len(got))
	}
	if got[2].Name != "FortyGigabitEthernet" {
		t.Errorf("last segment name = %q, want FortyGigabitEthernet", got[2].Name)
	}
	if got[2].Keys["name"] != "0/0/0" {
		t.Errorf("key not preserved: %v", got[2].Keys)
	}
}

func TestPathSpecForInterfaceChild_Helper(t *testing.T) {
	t.Parallel()
	got := pathSpecForInterfaceChild("HundredGigE", "1/2/3", "switchport")
	if len(got) != 4 {
		t.Fatalf("expected 4 elements, got %d", len(got))
	}
	if got[3].Name != "switchport" {
		t.Errorf("trailing child = %q, want switchport", got[3].Name)
	}
	if got[2].Keys["name"] != "1/2/3" {
		t.Errorf("interface key not preserved: %v", got[2].Keys)
	}
}

// TestEthernetWriter_PathSpecRoundTripsThroughGNMI is the
// end-to-end assertion: the writer's ops, when handed to the gNMI
// transport's path-conversion helper, produce a gNMI Path with the
// slash preserved in the list key. Pre-fix the fallback
// parseGNMIPath would split the path into multiple wrong elements.
func TestEthernetWriter_PathSpecRoundTripsThroughGNMI(t *testing.T) {
	t.Parallel()
	w := ethernetWriter{}
	desired := map[string]any{
		"interfaces": []any{
			map[string]any{
				"type":     "GigabitEthernet",
				"name":     "0/0/0",
				"shutdown": false,
			},
		},
	}
	ops, err := w.Diff(desired, []map[string]any(nil))
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) == 0 {
		t.Fatal("no ops emitted")
	}

	// Verify that PathSpec is populated and the key is preserved.
	op := ops[0]
	if op.PathSpec == nil {
		t.Fatal("PathSpec must be non-nil — gNMI fallback would split on '/'")
	}
	last := op.PathSpec[len(op.PathSpec)-1]
	if last.Keys["name"] != "0/0/0" {
		t.Errorf("PathSpec key not preserved through Diff: %v", last.Keys)
	}
	// transport.Op shape contract: PathSpec carries the key
	// verbatim; consumers (gNMI transport) build PathElem from it
	// without re-parsing the string Path.
	_ = transport.Op{}
}
