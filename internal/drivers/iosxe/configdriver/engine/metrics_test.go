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

package engine

import (
	"fmt"
	"testing"
)

func TestCapDriftBelowLimit(t *testing.T) {
	// Below-cap input must pass through unchanged so the common
	// path (no truncation) doesn't allocate or rewrite the slice.
	in := []DriftEntry{
		{Family: "vlan", Path: "/v"},
		{Family: "vrf", Path: "/r"},
	}
	out, dropped := CapDrift(in)
	if dropped != 0 {
		t.Fatalf("dropped=%d, want 0", dropped)
	}
	if len(out) != 2 {
		t.Fatalf("len=%d, want 2", len(out))
	}
	if &out[0] != &in[0] {
		t.Errorf("CapDrift should pass slice through unchanged when under cap")
	}
}

func TestCapDriftAtLimit(t *testing.T) {
	// Exactly-at-cap is the corner case where len > cap is false but
	// any off-by-one would either drop one or fail to drop one.
	in := make([]DriftEntry, MaxDriftEntries)
	for i := range in {
		in[i] = DriftEntry{Family: "vlan", Path: fmt.Sprintf("/v/%d", i)}
	}
	out, dropped := CapDrift(in)
	if dropped != 0 {
		t.Fatalf("dropped=%d, want 0", dropped)
	}
	if len(out) != MaxDriftEntries {
		t.Fatalf("len=%d, want %d", len(out), MaxDriftEntries)
	}
}

func TestCapDriftAboveLimit(t *testing.T) {
	// Three entries past the cap exercises the truncation path and
	// pins the dropped count to the exact overflow.
	const overflow = 3
	in := make([]DriftEntry, MaxDriftEntries+overflow)
	for i := range in {
		in[i] = DriftEntry{Family: "vlan", Path: fmt.Sprintf("/v/%d", i)}
	}
	out, dropped := CapDrift(in)
	if dropped != overflow {
		t.Fatalf("dropped=%d, want %d", dropped, overflow)
	}
	if len(out) != MaxDriftEntries {
		t.Fatalf("len=%d, want %d", len(out), MaxDriftEntries)
	}
	// Retained entries must be the head of the input, not a random
	// slice — surprises here would mask which families were kept.
	if out[0].Path != "/v/0" || out[MaxDriftEntries-1].Path != fmt.Sprintf("/v/%d", MaxDriftEntries-1) {
		t.Errorf("retained entries are not the head: first=%q last=%q",
			out[0].Path, out[MaxDriftEntries-1].Path)
	}
}

func TestCapDriftNilInput(t *testing.T) {
	// Nil-in / nil-out keeps the JSON-omitempty story intact for
	// the CR status — a freshly-applied InSync CR shouldn't write
	// "drift: []" into etcd.
	out, dropped := CapDrift(nil)
	if dropped != 0 || out != nil {
		t.Fatalf("nil input: out=%v dropped=%d", out, dropped)
	}
}

func TestRecordDriftTruncatedNoOpWhenUnregistered(t *testing.T) {
	// In unit tests we don't want a hidden requirement to
	// RegisterMetrics first — the engine package's metric helpers
	// must no-op cleanly when their var is nil. This is the safety
	// rail; if it ever panics we'd lose every test that touches
	// the engine without a metrics setup.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("RecordDriftTruncated panicked when metrics unregistered: %v", r)
		}
	}()
	RecordDriftTruncated("dev0", 7)
	RecordDriftTruncated("dev0", 0)
	RecordDriftTruncated("dev0", -1)
}
