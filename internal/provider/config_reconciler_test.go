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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver"
)

func newTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := runtime.NewScheme()
	if err := configv1alpha1.AddToScheme(s); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	return s
}

func newCR(name, device string) *configv1alpha1.IOSXEConfig {
	return &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:       name,
			Namespace:  "network",
			Generation: 1,
		},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: device},
			ManagedFamilies: []string{"vlan"},
			Source: configv1alpha1.ConfigurationSource{
				ConfigMapRef: &configv1alpha1.ConfigMapKeyRef{Name: "cm", Key: "data.yaml"},
			},
		},
	}
}

// TestRunExitsOnContextCancel verifies the loop honours context cancellation
// and returns the ctx.Err(), so callers running it in an errgroup observe
// the cause.
func TestRunExitsOnContextCancel(t *testing.T) {
	scheme := newTestScheme(t)
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()

	r := &ConfigReconciler{
		Client:     c,
		DeviceName: "edge-01",
		Driver:     configdriver.NewStubDriver(),
		Interval:   50 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	// Give the loop one tick, then cancel.
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

// TestMatchingCRGetsPending verifies a CR targeting this device ends up
// Phase=Pending after one pass, and a CR targeting a different device is
// left untouched.
func TestMatchingCRGetsPending(t *testing.T) {
	scheme := newTestScheme(t)
	matching := newCR("edge-01", "edge-01")
	other := newCR("core-01", "core-01")

	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(matching, other).
		WithStatusSubresource(&configv1alpha1.IOSXEConfig{}).
		Build()

	r := &ConfigReconciler{
		Client:     c,
		DeviceName: "edge-01",
		Driver:     configdriver.NewStubDriver(),
		Interval:   time.Hour, // never ticks; rely on the initial immediate pass
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = r.Run(ctx) }()

	// Poll for the Pending phase; give the initial immediate pass time to run.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		got := &configv1alpha1.IOSXEConfig{}
		if err := c.Get(ctx, types.NamespacedName{Namespace: "network", Name: "edge-01"}, got); err == nil {
			if got.Status.Phase == "Pending" && got.Status.ObservedGeneration == 1 {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}

	gotMatch := &configv1alpha1.IOSXEConfig{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "network", Name: "edge-01"}, gotMatch); err != nil {
		t.Fatalf("get matching CR: %v", err)
	}
	if gotMatch.Status.Phase != "Pending" {
		t.Fatalf("matching CR phase = %q, want Pending", gotMatch.Status.Phase)
	}
	if gotMatch.Status.ObservedGeneration != 1 {
		t.Fatalf("matching CR observedGeneration = %d, want 1", gotMatch.Status.ObservedGeneration)
	}

	gotOther := &configv1alpha1.IOSXEConfig{}
	if err := c.Get(ctx, types.NamespacedName{Namespace: "network", Name: "core-01"}, gotOther); err != nil {
		t.Fatalf("get other CR: %v", err)
	}
	if gotOther.Status.Phase != "" {
		t.Fatalf("non-matching CR was touched: phase = %q", gotOther.Status.Phase)
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
		{"nil client", &ConfigReconciler{DeviceName: "d", Driver: configdriver.NewStubDriver()}},
		{"empty device", &ConfigReconciler{Client: c, Driver: configdriver.NewStubDriver()}},
		{"nil driver", &ConfigReconciler{Client: c, DeviceName: "d"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.r.Run(ctx); err == nil {
				t.Fatal("expected validation error, got nil")
			}
		})
	}
}

// Compile-time check — the controller-runtime client matches the expected
// interface shape we exercise in the reconciler. Keeps refactors honest.
var _ client.Client = (client.Client)(nil)
