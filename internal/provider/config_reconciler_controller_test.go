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

	otelcodes "go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
)

// TestReconcileHandlesMissingCR verifies that a reconcile for a
// just-deleted CR (informer cache already empty) returns cleanly.
func TestReconcileHandlesMissingCR(t *testing.T) {
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}

	res, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "gone"},
	})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if res.Requeue || res.RequeueAfter != 0 {
		t.Errorf("expected clean result, got %+v", res)
	}
}

// TestReconcileIgnoresForeignDevice pins the defence-in-depth check:
// a CR somehow reaching Reconcile() but targeting another device is
// a no-op, not a spurious status write.
func TestReconcileIgnoresForeignDevice(t *testing.T) {
	scheme := newTestScheme(t)
	cr := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "stray", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef: configv1alpha1.DeviceRef{Name: "other-device"},
			IOSXEConfigTemplateSpec: configv1alpha1.IOSXEConfigTemplateSpec{
				ManagedFamilies: []string{"vlan"},
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "stray"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got configv1alpha1.IOSXEConfig
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "network", Name: "stray"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != "" {
		t.Fatalf("status touched on foreign device CR: phase=%q", got.Status.Phase)
	}
}

// TestReconcileNoTransportPathMarksPending verifies the manager-driven
// path sets Phase=Pending and Ready=False/NoTransport when the
// reconciler has no transport wired (scaffold deployment).
func TestReconcileNoTransportPathMarksPending(t *testing.T) {
	scheme := newTestScheme(t)
	device := newDevice("edge-01")
	cr := newCR("edge-01", "edge-01")
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(device, cr).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()
	r := &ConfigReconciler{Client: c, DeviceName: "edge-01"}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Namespace: "network", Name: "edge-01"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var got configv1alpha1.IOSXEConfig
	if err := c.Get(context.Background(),
		types.NamespacedName{Namespace: "network", Name: "edge-01"}, &got); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status.Phase != engine.PhasePending {
		t.Fatalf("phase=%q, want Pending", got.Status.Phase)
	}
	if !conditionIs(got.Status.Conditions, "Ready", metav1.ConditionFalse, "NoTransport") {
		t.Fatalf("Ready/NoTransport condition missing:\n%#v", got.Status.Conditions)
	}
}

func TestIOSXEConfigRootSpanReflectsRecordedBusinessFailure(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	_, span := provider.Tracer("test").Start(context.Background(), "config")
	recordIOSXEConfigSpanOutcome(span, engine.Result{
		Phase: engine.PhaseFailed,
		Err:   errors.New("intent resolution failed"),
	})
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans=%d, want 1", len(ended))
	}
	if got := ended[0].Status(); got.Code != otelcodes.Error {
		t.Fatalf("status=%#v, want Error", got)
	}
	attrs := map[string]string{}
	for _, kv := range ended[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	if attrs["cisco.vk.reconcile.outcome"] != "error" || attrs["cisco.vk.iosxeconfig.phase"] != engine.PhaseFailed {
		t.Fatalf("business failure attributes=%#v", attrs)
	}
}

func TestCommonConfigRootSpanReflectsRecordedBusinessFailure(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	_, span := provider.Tracer("test").Start(context.Background(), "config")
	recordCommonConfigSpanOutcome(span, engine.Result{
		Phase: engine.PhaseFailed,
		Err:   errors.New("writer lookup failed"),
	})
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 || ended[0].Status().Code != otelcodes.Error {
		t.Fatalf("common config spans=%d, want one Error span", len(ended))
	}
	attrs := map[string]string{}
	for _, kv := range ended[0].Attributes() {
		attrs[string(kv.Key)] = kv.Value.AsString()
	}
	if attrs["cisco.vk.reconcile.outcome"] != "error" || attrs["cisco.vk.config.phase"] != engine.PhaseFailed {
		t.Fatalf("business failure attributes=%#v", attrs)
	}
}
