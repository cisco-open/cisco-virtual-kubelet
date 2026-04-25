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
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
)

func TestBuildApplyLogEntryHashOnlyOnInSync(t *testing.T) {
	cr := newCR("edge-01", "edge-01")
	res := engine.Result{
		Phase: engine.PhaseInSync,
		FamilyStatuses: []engine.FamilyStatus{
			{Name: "vlan", State: "InSync", OpCount: 2},
		},
	}
	got := buildApplyLogEntry(cr, res, "sha256:abc")
	if got.Hash != "sha256:abc" {
		t.Errorf("hash=%q, want sha256:abc on InSync", got.Hash)
	}
	if got.Phase != engine.PhaseInSync {
		t.Errorf("phase=%q", got.Phase)
	}
	if len(got.Families) != 1 || got.Families[0].OpCount != 2 {
		t.Errorf("families=%#v", got.Families)
	}

	failed := engine.Result{Phase: engine.PhaseFailed}
	if e := buildApplyLogEntry(cr, failed, "sha256:would-have"); e.Hash != "" {
		t.Errorf("failed reconcile recorded hash %q; want empty", e.Hash)
	}
}

func TestAppendApplyLogTrimsToMaxEntries(t *testing.T) {
	// Seed an existing log CR at max=3 with two entries, then
	// append a third — that's exactly at-cap, no trim. The fourth
	// append must drop the oldest and bump TruncatedTotal.
	scheme := newTestScheme(t)
	logCR := &configv1alpha1.IOSXEConfigApplyLog{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		Spec: configv1alpha1.IOSXEConfigApplyLogSpec{
			DeviceRef:  configv1alpha1.DeviceRef{Name: "edge-01"},
			MaxEntries: 3,
		},
		Status: configv1alpha1.IOSXEConfigApplyLogStatus{
			Entries: []configv1alpha1.ApplyLogEntry{
				{Time: metav1.Now(), Phase: engine.PhaseInSync, SourceCR: "network/old@1"},
				{Time: metav1.Now(), Phase: engine.PhaseInSync, SourceCR: "network/recent@1"},
			},
		},
	}
	cr := newCR("edge-01", "edge-01")
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(logCR).
		WithStatusSubresource(&configv1alpha1.IOSXEConfigApplyLog{}).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}

	res := engine.Result{Phase: engine.PhaseInSync}
	if err := r.appendApplyLog(context.Background(), cr, res, "sha256:1", nil); err != nil {
		t.Fatalf("first append: %v", err)
	}
	if err := r.appendApplyLog(context.Background(), cr, res, "sha256:2", nil); err != nil {
		t.Fatalf("second append: %v", err)
	}

	var got configv1alpha1.IOSXEConfigApplyLog
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "network", Name: "edge-01"}, &got); err != nil {
		t.Fatalf("get log: %v", err)
	}
	if len(got.Status.Entries) != 3 {
		t.Fatalf("entries=%d, want 3 (capped)", len(got.Status.Entries))
	}
	if got.Status.TruncatedTotal != 1 {
		t.Errorf("TruncatedTotal=%d, want 1 (one drop after seeing 4 total)", got.Status.TruncatedTotal)
	}
	if got.Status.Entries[len(got.Status.Entries)-1].Hash != "sha256:2" {
		t.Errorf("most recent entry not preserved: %#v", got.Status.Entries)
	}
}

func TestAppendApplyLogStoresBodyOnlyWhenRetainBodyTrue(t *testing.T) {
	// RetainBody=false ⇒ entries carry no body even when the
	// reconciler hands a non-nil resolved intent. Cap-aware
	// behaviour stays unchanged.
	scheme := newTestScheme(t)
	logCR := &configv1alpha1.IOSXEConfigApplyLog{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		Spec: configv1alpha1.IOSXEConfigApplyLogSpec{
			DeviceRef:  configv1alpha1.DeviceRef{Name: "edge-01"},
			MaxEntries: 5,
			// RetainBody defaults to false.
		},
	}
	cr := newCR("edge-01", "edge-01")
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(logCR).
		WithStatusSubresource(&configv1alpha1.IOSXEConfigApplyLog{}).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	resolved := &intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{"vlans": []any{
			map[string]any{"id": float64(10), "name": "users"},
		}}},
	}
	if err := r.appendApplyLog(context.Background(), cr,
		engine.Result{Phase: engine.PhaseInSync}, "sha256:abc", resolved); err != nil {
		t.Fatalf("appendApplyLog: %v", err)
	}
	var got configv1alpha1.IOSXEConfigApplyLog
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "network", Name: "edge-01"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if len(got.Status.Entries) != 1 {
		t.Fatalf("entries=%d", len(got.Status.Entries))
	}
	if got.Status.Entries[0].Body != "" {
		t.Errorf("body should be empty when RetainBody=false: %q", got.Status.Entries[0].Body)
	}
}

func TestAppendApplyLogStoresBodyWhenRetainBodyTrue(t *testing.T) {
	scheme := newTestScheme(t)
	logCR := &configv1alpha1.IOSXEConfigApplyLog{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01", Namespace: "network"},
		Spec: configv1alpha1.IOSXEConfigApplyLogSpec{
			DeviceRef:  configv1alpha1.DeviceRef{Name: "edge-01"},
			MaxEntries: 5,
			RetainBody: true,
		},
	}
	cr := newCR("edge-01", "edge-01")
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(logCR).
		WithStatusSubresource(&configv1alpha1.IOSXEConfigApplyLog{}).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	resolved := &intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{"vlans": []any{
			map[string]any{"id": float64(10), "name": "users"},
		}}},
	}
	if err := r.appendApplyLog(context.Background(), cr,
		engine.Result{Phase: engine.PhaseInSync}, "sha256:abc", resolved); err != nil {
		t.Fatalf("appendApplyLog: %v", err)
	}
	var got configv1alpha1.IOSXEConfigApplyLog
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "network", Name: "edge-01"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	body := got.Status.Entries[0].Body
	if body == "" {
		t.Fatal("RetainBody=true should populate Body")
	}
	if !strings.Contains(body, `"vlan"`) || !strings.Contains(body, `"id":10`) {
		t.Errorf("body missing expected fields: %s", body)
	}
}

func TestAppendApplyLogIsNoopWhenNoLogCR(t *testing.T) {
	// Auditing is opt-in: a device with no IOSXEConfigApplyLog CR
	// is silently skipped. The reconciler must not error.
	scheme := newTestScheme(t)
	cr := newCR("edge-01", "edge-01")
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	if err := r.appendApplyLog(context.Background(), cr, engine.Result{Phase: engine.PhaseInSync}, "sha256:x", nil); err != nil {
		t.Fatalf("appendApplyLog: %v", err)
	}
}

func TestAppendApplyLogSkipsOtherDevicesLogs(t *testing.T) {
	// Cross-device safety: a log CR for edge-02 must not pick up
	// reconcile entries from edge-01's CR.
	scheme := newTestScheme(t)
	otherLog := &configv1alpha1.IOSXEConfigApplyLog{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-02-log", Namespace: "network"},
		Spec: configv1alpha1.IOSXEConfigApplyLogSpec{
			DeviceRef:  configv1alpha1.DeviceRef{Name: "edge-02"},
			MaxEntries: 5,
		},
	}
	cr := newCR("edge-01", "edge-01")
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(otherLog).
		WithStatusSubresource(&configv1alpha1.IOSXEConfigApplyLog{}).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	if err := r.appendApplyLog(context.Background(), cr, engine.Result{Phase: engine.PhaseInSync}, "sha256:x", nil); err != nil {
		t.Fatalf("appendApplyLog: %v", err)
	}
	var got configv1alpha1.IOSXEConfigApplyLog
	_ = c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "edge-02-log"}, &got)
	if len(got.Status.Entries) != 0 {
		t.Errorf("foreign log got %d entries; want 0", len(got.Status.Entries))
	}
}
