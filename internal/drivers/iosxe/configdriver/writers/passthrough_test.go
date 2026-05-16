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

import (
	"encoding/json"
	"testing"
)

func TestLeafPassthroughPreserved(t *testing.T) {
	// Current untyped writers build merge payloads from maps, so leaves
	// present in desired intent must pass through unchanged. Phase-5 typed
	// writer migrations must keep this invariant green.
	w := Get("snmp_server")
	if w == nil {
		t.Fatal("snmp_server writer is not registered")
	}

	desired := map[string]any{
		"location":               "lab-a",
		"some_unmodelled_leaf":   "foo",
		"some_unmodelled_number": float64(42),
	}
	observed := map[string]any{
		"location": "old-lab",
	}

	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("op count = %d, want 1: %#v", len(ops), ops)
	}

	var body map[string]map[string]any
	if err := json.Unmarshal(ops[0].Body, &body); err != nil {
		t.Fatalf("decode op body: %v", err)
	}
	payload := body["Cisco-IOS-XE-snmp:snmp-server"]
	if payload["some_unmodelled_leaf"] != "foo" {
		t.Fatalf("unmodelled string leaf = %#v, want foo; payload=%#v",
			payload["some_unmodelled_leaf"], payload)
	}
	if payload["some_unmodelled_number"] != float64(42) {
		t.Fatalf("unmodelled numeric leaf = %#v, want 42; payload=%#v",
			payload["some_unmodelled_number"], payload)
	}
}
