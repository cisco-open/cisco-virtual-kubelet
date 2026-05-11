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

package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
)

func TestSplitReplayAnnotationAcceptsHashSelector(t *testing.T) {
	// "edge-01-log:sha256:abc" splits as
	// ["edge-01-log", "sha256:abc"] — the hash selector itself
	// carries a colon, and a naive split would mangle it.
	got := splitReplayAnnotation("edge-01-log:sha256:abc123")
	if got == nil || got[0] != "edge-01-log" || got[1] != "sha256:abc123" {
		t.Errorf("got %#v, want [edge-01-log sha256:abc123]", got)
	}
}

func TestSplitReplayAnnotationRejectsMalformed(t *testing.T) {
	for _, raw := range []string{"", ":", "no-colon", "trailing:", ":leading"} {
		if got := splitReplayAnnotation(raw); got != nil {
			t.Errorf("splitReplayAnnotation(%q)=%v, want nil", raw, got)
		}
	}
}

func TestPickReplayEntryByIndex(t *testing.T) {
	entries := []configv1alpha1.ApplyLogEntry{
		{Hash: "sha256:0", Body: "a"},
		{Hash: "sha256:1", Body: "b"},
		{Hash: "sha256:2", Body: "c"},
	}
	got, err := pickReplayEntry(entries, "1")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if got.Hash != "sha256:1" {
		t.Errorf("got %q, want sha256:1", got.Hash)
	}
	if _, err := pickReplayEntry(entries, "3"); err == nil {
		t.Errorf("out-of-range index should error")
	}
}

func TestPickReplayEntryByHash(t *testing.T) {
	entries := []configv1alpha1.ApplyLogEntry{
		{Hash: "sha256:zero"},
		{Hash: "sha256:one"},
	}
	got, err := pickReplayEntry(entries, "sha256:one")
	if err != nil {
		t.Fatalf("pick: %v", err)
	}
	if got.Hash != "sha256:one" {
		t.Errorf("got %q, want sha256:one", got.Hash)
	}
	if _, err := pickReplayEntry(entries, "sha256:nope"); err == nil {
		t.Errorf("missing hash should error")
	}
}

func TestApplyReplayAnnotationOverridesResolved(t *testing.T) {
	// CR carries the replay annotation pointing at a log entry
	// whose body declares vlan 99. The reconciler must override
	// the resolved configuration with that body so the engine
	// applies the historical shape.
	scheme := newTestScheme(t)
	body, err := encodeReplayBody(&intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{
			"vlans": []any{map[string]any{"id": float64(99), "name": "from-history"}},
		}},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	logCR := &configv1alpha1.IOSXEConfigApplyLog{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		Spec: configv1alpha1.IOSXEConfigApplyLogSpec{
			DeviceRef:  configv1alpha1.DeviceRef{Name: "edge-01"},
			MaxEntries: 5, RetainBody: true,
		},
		Status: configv1alpha1.IOSXEConfigApplyLogStatus{
			Entries: []configv1alpha1.ApplyLogEntry{
				{Hash: "sha256:hist", Body: body},
			},
		},
	}
	cr := newCR("edge-01", "edge-01")
	cr.Annotations = map[string]string{ReplayAnnotation: "edge-01:0"}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(logCR, cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfigApplyLog{}, &configv1alpha1.IOSXEConfig{}).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}

	resolved := &intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{
			"vlans": []any{map[string]any{"id": float64(10), "name": "current"}},
		}},
	}
	got, applied, err := r.applyReplayAnnotation(context.Background(), cr, resolved)
	if err != nil {
		t.Fatalf("applyReplayAnnotation: %v", err)
	}
	if !applied {
		t.Fatal("annotation present and resolvable; applied should be true")
	}
	vlans, _ := got.Configuration["vlan"].(map[string]any)["vlans"].([]any)
	if len(vlans) == 0 {
		t.Fatalf("override produced no vlans: %#v", got.Configuration)
	}
	first := vlans[0].(map[string]any)
	if first["id"] != float64(99) || first["name"] != "from-history" {
		t.Errorf("override didn't replace current intent: %#v", first)
	}
}

func TestApplyReplayAnnotationFailsLoudOnMissingBody(t *testing.T) {
	// RetainBody=false log: entries exist but Body is empty.
	// Replay must error rather than silently no-op so the
	// operator sees the misconfiguration.
	scheme := newTestScheme(t)
	logCR := &configv1alpha1.IOSXEConfigApplyLog{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		Spec: configv1alpha1.IOSXEConfigApplyLogSpec{
			DeviceRef:  configv1alpha1.DeviceRef{Name: "edge-01"},
			MaxEntries: 5,
		},
		Status: configv1alpha1.IOSXEConfigApplyLogStatus{
			Entries: []configv1alpha1.ApplyLogEntry{{Hash: "sha256:none"}},
		},
	}
	cr := newCR("edge-01", "edge-01")
	cr.Annotations = map[string]string{ReplayAnnotation: "edge-01:0"}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(logCR, cr).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	_, _, err := r.applyReplayAnnotation(context.Background(), cr, &intent.ResolvedIntent{})
	if err == nil || !strings.Contains(err.Error(), "no retained body") {
		t.Fatalf("got %v, want missing-body error", err)
	}
}

func TestApplyReplayAnnotationNoOpWhenAbsent(t *testing.T) {
	// The fast path: no annotation, return resolved verbatim,
	// applied=false, no Get-list calls. This is what the
	// reconciler hits on every steady-state tick.
	scheme := newTestScheme(t)
	cr := newCR("edge-01", "edge-01")
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	resolved := &intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{}},
	}
	got, applied, err := r.applyReplayAnnotation(context.Background(), cr, resolved)
	if err != nil {
		t.Fatalf("applyReplayAnnotation: %v", err)
	}
	if applied {
		t.Error("applied should be false when no annotation present")
	}
	if got != resolved {
		t.Error("no-op path should return the same pointer")
	}
}

func TestApplyRollbackToOverridesResolvedIntent(t *testing.T) {
	scheme := newTestScheme(t)
	body, err := encodeReplayBody(&intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{
			"vlans": []any{map[string]any{"id": float64(99), "name": "from-revision"}},
		}},
	})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	cr := newCR("edge-01", "edge-01")
	cr.UID = types.UID("config-uid")
	cr.Spec.RollbackTo = "edge-01-rev-1"
	rev := &configv1alpha1.IOSXEConfigRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01-rev-1", Namespace: "network"},
		Spec: configv1alpha1.IOSXEConfigRevisionSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
			SourceRef: "network/edge-01",
			SourceUID: "config-uid",
			Hash:      "sha256:hist",
			Body:      body,
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr, rev).Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	resolved := &intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{
			"vlans": []any{map[string]any{"id": float64(10), "name": "current"}},
		}},
	}
	got, applied, target, err := r.applyRollbackTo(context.Background(), cr, resolved)
	if err != nil {
		t.Fatalf("applyRollbackTo: %v", err)
	}
	if !applied {
		t.Fatal("rollbackTo set; applied should be true")
	}
	if target != "edge-01-rev-1" {
		t.Fatalf("target=%q want edge-01-rev-1", target)
	}
	vlans, _ := got.Configuration["vlan"].(map[string]any)["vlans"].([]any)
	if len(vlans) == 0 {
		t.Fatalf("rollback produced no vlans: %#v", got.Configuration)
	}
	first := vlans[0].(map[string]any)
	if first["id"] != float64(99) || first["name"] != "from-revision" {
		t.Errorf("rollback didn't replace current intent: %#v", first)
	}
}

func TestAppendConfigRevisionCreatesAndPrunesOldest(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("edge-01", "edge-01")
	cr.UID = types.UID("config-uid")
	cr.Generation = 3
	cr.Spec.RevisionHistoryLimit = ptr.To[int32](2)
	old := func(name string, ts time.Time) *configv1alpha1.IOSXEConfigRevision {
		return &configv1alpha1.IOSXEConfigRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "network",
				CreationTimestamp: metav1.NewTime(ts),
				Labels: map[string]string{
					revisionSourceNameLabel: "edge-01",
					revisionSourceUIDLabel:  "config-uid",
				},
			},
			Spec: configv1alpha1.IOSXEConfigRevisionSpec{
				DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
				SourceRef: "network/edge-01",
				SourceUID: "config-uid",
				Hash:      "sha256:" + name,
				Body:      `{"v":1,"configuration":{}}`,
			},
		}
	}
	now := time.Now().UTC()
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(
			old("oldest", now.Add(-2*time.Hour)),
			old("middle", now.Add(-1*time.Hour)),
		).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	resolved := &intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{"vlans": []any{
			map[string]any{"id": float64(10), "name": "users"},
		}}},
	}
	if err := r.appendConfigRevision(context.Background(), cr,
		engine.Result{Phase: engine.PhaseInSync}, "sha256:newest", resolved,
		map[string][]string{"vlan": {"10"}}); err != nil {
		t.Fatalf("appendConfigRevision: %v", err)
	}
	var list configv1alpha1.IOSXEConfigRevisionList
	if err := c.List(context.Background(), &list); err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("revisions=%d want 2: %#v", len(list.Items), list.Items)
	}
	names := map[string]bool{}
	for _, item := range list.Items {
		names[item.Name] = true
	}
	if names["oldest"] {
		t.Fatalf("oldest revision was not pruned: %#v", names)
	}
	if !names["middle"] || !names[revisionName(cr, "sha256:newest")] {
		t.Fatalf("expected middle and newest revisions, got %#v", names)
	}
}

func TestAppendConfigRevisionUnsetHistoryLimitDefaultsToTen(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("edge-01", "edge-01")
	cr.UID = types.UID("config-uid")
	cr.Generation = 3
	old := func(name string, ts time.Time) *configv1alpha1.IOSXEConfigRevision {
		return &configv1alpha1.IOSXEConfigRevision{
			ObjectMeta: metav1.ObjectMeta{
				Name:              name,
				Namespace:         "network",
				CreationTimestamp: metav1.NewTime(ts),
				Labels: map[string]string{
					revisionSourceNameLabel: "edge-01",
					revisionSourceUIDLabel:  "config-uid",
				},
			},
			Spec: configv1alpha1.IOSXEConfigRevisionSpec{
				DeviceRef: configv1alpha1.DeviceRef{Name: "edge-01"},
				SourceRef: "network/edge-01",
				SourceUID: "config-uid",
				Hash:      "sha256:" + name,
				Body:      `{"v":1,"configuration":{}}`,
			},
		}
	}
	now := time.Now().UTC()
	existing := make([]client.Object, 0, 10)
	for i := 0; i < 10; i++ {
		existing = append(existing, old(fmt.Sprintf("old-%02d", i), now.Add(time.Duration(i-10)*time.Minute)))
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existing...).Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	resolved := &intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{"vlans": []any{
			map[string]any{"id": float64(10), "name": "users"},
		}}},
	}
	if err := r.appendConfigRevision(context.Background(), cr,
		engine.Result{Phase: engine.PhaseInSync}, "sha256:newest-zero-default", resolved,
		nil); err != nil {
		t.Fatalf("appendConfigRevision: %v", err)
	}
	var list configv1alpha1.IOSXEConfigRevisionList
	if err := c.List(context.Background(), &list); err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(list.Items) != int(defaultRevisionHistoryLimit) {
		t.Fatalf("revisions=%d want %d: %#v", len(list.Items), defaultRevisionHistoryLimit, list.Items)
	}
	names := map[string]bool{}
	for _, item := range list.Items {
		names[item.Name] = true
	}
	if !names[revisionName(cr, "sha256:newest-zero-default")] {
		t.Fatalf("new revision missing with zero history limit default: %#v", names)
	}
	if names["old-00"] {
		t.Fatalf("oldest revision was not pruned under default limit: %#v", names)
	}
}

func TestAppendConfigRevisionExplicitZeroDisablesRevisions(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("edge-01", "edge-01")
	cr.UID = types.UID("config-uid")
	cr.Generation = 4
	cr.Spec.RevisionHistoryLimit = ptr.To[int32](0)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	resolved := &intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{"vlans": []any{
			map[string]any{"id": float64(20), "name": "guests"},
		}}},
	}
	if err := r.appendConfigRevision(context.Background(), cr,
		engine.Result{Phase: engine.PhaseInSync}, "sha256:should-not-persist", resolved,
		nil); err != nil {
		t.Fatalf("appendConfigRevision: %v", err)
	}
	var list configv1alpha1.IOSXEConfigRevisionList
	if err := c.List(context.Background(), &list); err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(list.Items) != 0 {
		t.Fatalf("revisions=%d want 0 (explicit zero disables): %#v", len(list.Items), list.Items)
	}
}
