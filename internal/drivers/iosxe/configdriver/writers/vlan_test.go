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
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

func newTestTransport(t *testing.T, h http.HandlerFunc) (transport.Interface, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	cli, err := transport.NewRESTCONF(transport.RESTCONFConfig{
		BaseURL: srv.URL, HTTPClient: srv.Client(), Username: "u", Password: "p",
	})
	if err != nil {
		t.Fatalf("NewRESTCONF: %v", err)
	}
	return cli, srv
}

func mkVLAN(id int, name string) map[string]any {
	return map[string]any{"id": id, "name": name}
}

func TestVLANDiffCreatesMissing(t *testing.T) {
	t.Parallel()
	w := vlanWriter{}
	desired := []map[string]any{mkVLAN(10, "users"), mkVLAN(20, "voice")}
	observed := []map[string]any{}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("got %d ops, want 2", len(ops))
	}
	for _, op := range ops {
		if op.Verb != transport.VerbMerge {
			t.Errorf("verb=%v, want MERGE", op.Verb)
		}
	}
}

func TestVLANDiffNoChangeOnEqual(t *testing.T) {
	t.Parallel()
	w := vlanWriter{}
	list := []map[string]any{mkVLAN(10, "users"), mkVLAN(20, "voice")}
	ops, err := w.Diff(list, list)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0 (no drift)", len(ops))
	}
}

func TestVLANDiffUpdatesChangedLeaf(t *testing.T) {
	t.Parallel()
	w := vlanWriter{}
	desired := []map[string]any{mkVLAN(10, "users"), {"id": 20, "name": "VOICE"}}
	observed := []map[string]any{mkVLAN(10, "users"), mkVLAN(20, "voice")}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1 (only vlan 20 changed)", len(ops))
	}
	if !strings.Contains(ops[0].Path, "=20") {
		t.Errorf("op path = %q, want vlan id 20 target", ops[0].Path)
	}
}

func TestVLANDiffIgnoresExtraObservedVLANs(t *testing.T) {
	t.Parallel()
	// A VLAN on the device that is not in the intent must NOT be deleted.
	// Pruning is a whole-family-level decision (spec.pruneOnRelinquish).
	w := vlanWriter{}
	desired := []map[string]any{mkVLAN(10, "users")}
	observed := []map[string]any{mkVLAN(10, "users"), mkVLAN(99, "stray")}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0 (writer is additive)", len(ops))
	}
}

func TestVLANPruneDiffEmitsDeletesForObservedNotInDesired(t *testing.T) {
	t.Parallel()
	// PruneDiff is the opt-in counterpart to Diff: when the CR sets
	// spec.pruneOnRelinquish: true, the engine consults this to learn
	// which device entries no longer have a home in the intent. We
	// must emit one VerbDelete per orphan, in deterministic id order
	// so equivalent diffs are byte-equal.
	w := vlanWriter{}
	desired := []map[string]any{mkVLAN(10, "users")}
	observed := []map[string]any{mkVLAN(10, "users"), mkVLAN(99, "stray"), mkVLAN(50, "old")}
	ops, err := w.PruneDiff(desired, observed)
	if err != nil {
		t.Fatalf("PruneDiff: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("got %d ops, want 2", len(ops))
	}
	for _, op := range ops {
		if op.Verb != transport.VerbDelete {
			t.Errorf("op verb=%q, want VerbDelete", op.Verb)
		}
	}
	if !strings.HasSuffix(ops[0].Path, "=50") || !strings.HasSuffix(ops[1].Path, "=99") {
		t.Errorf("paths not in id order: %s, %s", ops[0].Path, ops[1].Path)
	}
}

func TestVLANPruneDiffEmptyWhenObservedIsSubset(t *testing.T) {
	t.Parallel()
	// Observed is a subset of desired ⇒ nothing to prune. Important:
	// PruneDiff must not emit ops simply because the device is
	// missing entries — that's Diff's job (additive).
	w := vlanWriter{}
	desired := []map[string]any{mkVLAN(10, "users"), mkVLAN(20, "voip")}
	observed := []map[string]any{mkVLAN(10, "users")}
	ops, err := w.PruneDiff(desired, observed)
	if err != nil {
		t.Fatalf("PruneDiff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0", len(ops))
	}
}

func TestVLANDiffIgnoresUnmanagedLeavesOnObserved(t *testing.T) {
	t.Parallel()
	// Device returns extra leaves (e.g. remote-span, device-tracking)
	// that the Phase-1 writer does not model. It must NOT treat them as
	// drift — otherwise the family would read as perpetually Drifted.
	w := vlanWriter{}
	desired := []map[string]any{mkVLAN(10, "users")}
	observed := []map[string]any{{
		"id": 10, "name": "users", "remote-span": true, "device-tracking": "enabled",
	}}
	ops, err := w.Diff(desired, observed)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 0 {
		t.Fatalf("got %d ops, want 0 (unmanaged leaves ignored)", len(ops))
	}
}

func TestVLANDiffDeterministicOrdering(t *testing.T) {
	t.Parallel()
	w := vlanWriter{}
	desired := []map[string]any{mkVLAN(30, "c"), mkVLAN(10, "a"), mkVLAN(20, "b")}
	ops, err := w.Diff(desired, nil)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	wantSuffixes := []string{"=10", "=20", "=30"}
	if len(ops) != len(wantSuffixes) {
		t.Fatalf("got %d ops, want %d", len(ops), len(wantSuffixes))
	}
	for i, want := range wantSuffixes {
		if !strings.HasSuffix(ops[i].Path, want) {
			t.Errorf("ops[%d].Path=%q, want suffix %q", i, ops[i].Path, want)
		}
	}
}

func TestVLANDiffInvalidIdFails(t *testing.T) {
	t.Parallel()
	w := vlanWriter{}
	_, err := w.Diff([]map[string]any{{"name": "nokey"}}, nil)
	if err == nil || !strings.Contains(err.Error(), "missing 'id'") {
		t.Fatalf("got %v, want missing-id error", err)
	}
}

func TestVLANDiffSupportsYAMLDecodedShape(t *testing.T) {
	t.Parallel()
	// YAML decoders typically produce []any with map[string]any elements
	// and float64 numbers. The writer must accept that shape without a
	// pre-normalisation step at the engine.
	w := vlanWriter{}
	desired := []any{
		map[string]any{"id": float64(10), "name": "users"},
	}
	ops, err := w.Diff(desired, []any{})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("got %d ops, want 1", len(ops))
	}
}

func TestVLANFetchStripsYANGWrapper(t *testing.T) {
	t.Parallel()
	cli, _ := newTestTransport(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"Cisco-IOS-XE-vlan:vlan-list":[{"id":10,"name":"users"}]}`)
	})
	got, err := vlanWriter{}.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	list := got.([]map[string]any)
	if len(list) != 1 || list[0]["id"].(float64) != 10 {
		t.Fatalf("unexpected decode: %#v", list)
	}
}

func TestVLANFetchEmptyOn404(t *testing.T) {
	t.Parallel()
	cli, _ := newTestTransport(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errors":{"error":[]}}`, http.StatusNotFound)
	})
	got, err := vlanWriter{}.Fetch(context.Background(), cli)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	list := got.([]map[string]any)
	if len(list) != 0 {
		t.Fatalf("got %d VLANs, want 0 on 404", len(list))
	}
}

func TestVLANApplyRoundTrip(t *testing.T) {
	t.Parallel()
	var received []string
	cli, _ := newTestTransport(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = append(received, r.Method+" "+r.URL.Path+" "+string(body))
		w.WriteHeader(http.StatusNoContent)
	})
	ops, err := vlanWriter{}.Diff(
		[]map[string]any{mkVLAN(10, "users"), mkVLAN(20, "voice")},
		[]map[string]any{},
	)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	w := vlanWriter{}
	if err := w.Apply(context.Background(), cli, ops); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(received) != 2 {
		t.Fatalf("got %d requests, want 2", len(received))
	}
	// Verify payload shape for the first op.
	body := received[0][strings.Index(received[0], "{"):]
	var envelope map[string]any
	if err := json.Unmarshal([]byte(body), &envelope); err != nil {
		t.Fatalf("decode payload: %v\nbody=%s", err, body)
	}
	entry := envelope["Cisco-IOS-XE-vlan:vlan-list"].([]any)[0].(map[string]any)
	want := map[string]any{"id": float64(10), "name": "users"}
	if !reflect.DeepEqual(entry, want) {
		t.Fatalf("entry=%#v, want %#v", entry, want)
	}
}

func TestVLANApplyNoopOnEmptyOps(t *testing.T) {
	t.Parallel()
	// A Diff that returns zero ops must produce zero HTTP traffic on
	// Apply; important for the hash-short-circuit story.
	var requests int
	cli, _ := newTestTransport(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
	})
	w := vlanWriter{}
	if err := w.Apply(context.Background(), cli, nil); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if requests != 0 {
		t.Fatalf("got %d requests, want 0", requests)
	}
}

// Compile-time pin: the real writer satisfies SectionWriter.
var _ SectionWriter = (vlanWriter{})
