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
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := configv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("config AddToScheme: %v", err)
	}
	if err := ciskov1.AddToScheme(s); err != nil {
		t.Fatalf("cisko AddToScheme: %v", err)
	}
	if err := corev1.AddToScheme(s); err != nil {
		t.Fatalf("core AddToScheme: %v", err)
	}
	return s
}

func newCR(name, device string) *configv1alpha1.IOSXEConfig {
	return &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "network", Generation: 1,
		},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: device},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"vlan"},
				Source: configv1alpha1.ConfigurationSource{
					Inline: &runtime.RawExtension{Raw: []byte(`{}`)},
				},
			},
		},
	}
}

func newDevice(name string) *ciskov1.CiscoDevice {
	return &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "network"},
		Spec: ciskov1.DeviceSpec{
			Driver: ciskov1.DeviceDriverXE, Address: "10.0.0.1", Username: "u",
		},
	}
}

// TestRunExitsOnContextCancel verifies the loop honours context cancellation.
func TestRunExitsOnContextCancel(t *testing.T) {
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()

	r := &ConfigReconciler{
		Client:     c,
		DeviceName: "edge-01",
		Interval:   50 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	time.Sleep(75 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run returned %v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not exit within 500ms of cancel")
	}
}

// With no transport wired (scaffold path), matched CRs land in Pending
// with a Ready=False / NoTransport condition rather than Failed. This
// is the contract that keeps apphosting unaffected when the config
// driver hasn't been provisioned with a transport yet.
func TestMatchingCRGetsPendingWhenNoTransport(t *testing.T) {
	scheme := newTestScheme(t)
	matching := newCR("edge-01", "edge-01")
	other := newCR("core-01", "core-01")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(newDevice("edge-01"), matching, other).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()

	r := &ConfigReconciler{
		Client: c, DeviceName: "edge-01", Interval: time.Hour,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		got := &configv1alpha1.IOSXEConfig{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: "network", Name: "edge-01"}, got); err == nil {
			if got.Status.Phase == engine.PhasePending && got.Status.ObservedGeneration == 1 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	matched := &configv1alpha1.IOSXEConfig{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "network", Name: "edge-01"}, matched); err != nil {
		t.Fatalf("get matching CR: %v", err)
	}
	if matched.Status.Phase != engine.PhasePending {
		t.Fatalf("matching CR phase=%q, want Pending", matched.Status.Phase)
	}
	if !conditionIs(matched.Status.Conditions, "Ready", metav1.ConditionFalse, "NoTransport") {
		t.Fatalf("Ready condition missing/NoTransport:\n%#v", matched.Status.Conditions)
	}

	otherCR := &configv1alpha1.IOSXEConfig{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "network", Name: "core-01"}, otherCR); err != nil {
		t.Fatalf("get other CR: %v", err)
	}
	if otherCR.Status.Phase != "" {
		t.Fatalf("non-matching CR was touched: phase=%q", otherCR.Status.Phase)
	}
}

func TestRunRejectsNilDependencies(t *testing.T) {
	ctx := context.Background()
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()

	cases := []struct {
		name string
		r    *ConfigReconciler
	}{
		{"nil client", &ConfigReconciler{DeviceName: "d"}},
		{"empty device", &ConfigReconciler{Client: c}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.r.Run(ctx); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

func TestRecordResultSetsRolledBackCondition(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("edge-01", "edge-01")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}

	result := engine.Result{
		Phase:         engine.PhaseInSync,
		DeviceTouched: true,
	}
	resolved := &intent.ResolvedIntent{ManagedFamilies: []string{"vlan"}}
	if err := r.recordResult(context.Background(), cr, result, "sha256:rollback", nil, resolved, "edge-01-rev-1"); err != nil {
		t.Fatalf("recordResult: %v", err)
	}

	var got configv1alpha1.IOSXEConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}, &got); err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got.Status.LastRollbackedTo == nil || *got.Status.LastRollbackedTo != "edge-01-rev-1" {
		t.Fatalf("LastRollbackedTo=%v, want edge-01-rev-1", got.Status.LastRollbackedTo)
	}
	if !conditionIs(got.Status.Conditions, "Rolled-Back", metav1.ConditionTrue, "RolledBack") {
		t.Fatalf("Rolled-Back condition missing/RolledBack:\n%#v", got.Status.Conditions)
	}
}

func TestRecordResultSetsRollbackFailedCondition(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("edge-01", "edge-01")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}

	result := engine.Result{
		Phase: engine.PhaseFailed,
		Err:   errors.New("apply failed"),
	}
	resolved := &intent.ResolvedIntent{ManagedFamilies: []string{"vlan"}}
	if err := r.recordResult(context.Background(), cr, result, "sha256:rollback", nil, resolved, "edge-01-rev-1"); err != nil {
		t.Fatalf("recordResult: %v", err)
	}

	var got configv1alpha1.IOSXEConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}, &got); err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got.Status.LastRollbackedTo != nil {
		t.Fatalf("LastRollbackedTo=%v, want nil on failed rollback", *got.Status.LastRollbackedTo)
	}
	if !conditionIs(got.Status.Conditions, "Rolled-Back", metav1.ConditionFalse, "RollbackFailed") {
		t.Fatalf("Rolled-Back condition missing/RollbackFailed:\n%#v", got.Status.Conditions)
	}
}

func TestAppendConfigRevisionWithSecretRefsOmitsSecretFamilies(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("edge-01", "edge-01")
	cr.UID = types.UID("config-uid")
	cr.Generation = 7
	cr.Spec.RevisionHistoryLimit = ptr.To[int32](5)
	cr.Spec.SecretRefs = []configv1alpha1.FamilySecretRef{{
		Family: "bgp",
		Name:   "bgp-creds",
		Key:    "intent.yaml",
	}}
	resolved := &intent.ResolvedIntent{
		Configuration: map[string]any{
			"vlan": map[string]any{"vlans": []any{map[string]any{"id": float64(10)}}},
			"bgp":  map[string]any{"neighbors": []any{map[string]any{"id": "192.0.2.1", "password": "secret"}}},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}

	if err := r.appendConfigRevision(context.Background(), cr, engine.Result{Phase: engine.PhaseInSync}, "sha256:secret", resolved, nil); err != nil {
		t.Fatalf("appendConfigRevision: %v", err)
	}

	var revs configv1alpha1.IOSXEConfigRevisionList
	if err := c.List(context.Background(), &revs); err != nil {
		t.Fatalf("list revisions: %v", err)
	}
	if len(revs.Items) != 1 {
		t.Fatalf("revisions=%d want 1", len(revs.Items))
	}
	rev := revs.Items[0]
	if !rev.Spec.SecretMaterialOmitted {
		t.Fatal("SecretMaterialOmitted=false want true")
	}
	if len(rev.Spec.SecretRefNames) != 1 || rev.Spec.SecretRefNames[0] != "bgp-creds" {
		t.Fatalf("SecretRefNames=%v want [bgp-creds]", rev.Spec.SecretRefNames)
	}
	body, err := decodeReplayBody(rev.Spec.Body)
	if err != nil {
		t.Fatalf("decode revision body: %v", err)
	}
	if _, ok := body.Configuration["bgp"]; ok {
		t.Fatalf("secret-backed bgp family persisted in revision body: %#v", body.Configuration["bgp"])
	}
	if _, ok := body.Configuration["vlan"]; !ok {
		t.Fatalf("non-secret vlan family missing from revision body: %#v", body.Configuration)
	}
}

func TestRollbackBlockedWhenSecretRevisionNamesDiffer(t *testing.T) {
	scheme := newTestScheme(t)
	cr := newCR("edge-01", "edge-01")
	cr.UID = types.UID("config-uid")
	cr.Spec.RollbackTo = "edge-01-rev-1"
	cr.Spec.SecretRefs = []configv1alpha1.FamilySecretRef{{
		Family: "vlan",
		Name:   "new-secret",
		Key:    "intent.yaml",
	}}
	body, err := encodeReplayBody(&intent.ResolvedIntent{
		Configuration: map[string]any{"vlan": map[string]any{"vlans": []any{}}},
	})
	if err != nil {
		t.Fatalf("encode revision body: %v", err)
	}
	rev := &configv1alpha1.IOSXEConfigRevision{
		ObjectMeta: metav1.ObjectMeta{Name: "edge-01-rev-1", Namespace: "network"},
		Spec: configv1alpha1.IOSXEConfigRevisionSpec{
			DeviceRef:             configv1alpha1.DeviceRef{Name: "edge-01"},
			SourceRef:             "network/edge-01",
			SourceUID:             "config-uid",
			Hash:                  "sha256:old",
			Body:                  body,
			SecretMaterialOmitted: true,
			SecretRefNames:        []string{"old-secret"},
		},
	}
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "new-secret", Namespace: "network"},
		Data:       map[string][]byte{"intent.yaml": []byte(`{"vlans":[]}`)},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(newDevice("edge-01"), cr, rev, secret).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}
	resolver := &intent.Resolver{Client: c}
	eng := &engine.Engine{}

	result, err := r.reconcileOne(context.Background(), nil, resolver, eng, cr, nil, triggerEvent)
	if err != nil {
		t.Fatalf("reconcileOne record status: %v", err)
	}
	if result.Phase != engine.PhaseFailed {
		t.Fatalf("phase=%q want failed", result.Phase)
	}
	var got configv1alpha1.IOSXEConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: cr.Namespace, Name: cr.Name}, &got); err != nil {
		t.Fatalf("get config: %v", err)
	}
	if !conditionIs(got.Status.Conditions, "RollbackBlocked", metav1.ConditionTrue, "RevisionMissingSecretMaterial") {
		t.Fatalf("RollbackBlocked condition missing RevisionMissingSecretMaterial: %#v", got.Status.Conditions)
	}
}

func conditionIs(conds []metav1.Condition, t string, s metav1.ConditionStatus, reason string) bool {
	for _, c := range conds {
		if c.Type == t && c.Status == s && c.Reason == reason {
			return true
		}
	}
	return false
}
