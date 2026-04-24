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

package controller

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func bundleScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	s := newTestScheme(t)
	utilruntime.Must(configv1alpha1.AddToScheme(s))
	return s
}

func mkBundle(name, ns string, sel map[string]string, refs ...string) *configv1alpha1.IOSXEConfigBundle {
	b := &configv1alpha1.IOSXEConfigBundle{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Generation: 1},
		Spec: configv1alpha1.IOSXEConfigBundleSpec{
			Template: configv1alpha1.IOSXEConfigSpec{
				ManagedFamilies: []string{"vlan"},
				Source: configv1alpha1.ConfigurationSource{
					Inline: &runtime.RawExtension{Raw: []byte(`{}`)},
				},
			},
		},
	}
	if len(sel) > 0 {
		b.Spec.DeviceSelector = &metav1.LabelSelector{MatchLabels: sel}
	}
	for _, r := range refs {
		b.Spec.DeviceRefs = append(b.Spec.DeviceRefs, configv1alpha1.DeviceRef{Name: r})
	}
	return b
}

func mkLabeledDevice(name, ns string, labels map[string]string) *ciskov1.CiscoDevice {
	d := newDevice(name, ns)
	d.Labels = labels
	return d
}

func TestBundleReconcileFanoutToSelectorMatches(t *testing.T) {
	scheme := bundleScheme(t)
	d1 := mkLabeledDevice("edge-01", "network", map[string]string{"role": "edge"})
	d2 := mkLabeledDevice("edge-02", "network", map[string]string{"role": "edge"})
	other := mkLabeledDevice("core-01", "network", map[string]string{"role": "core"})
	b := mkBundle("edges", "network", map[string]string{"role": "edge"})

	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(d1, d2, other, b).
		WithStatusSubresource(&configv1alpha1.IOSXEConfigBundle{}).
		Build()
	r := &IOSXEConfigBundleReconciler{Client: c, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "edges", Namespace: "network"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}

	var children configv1alpha1.IOSXEConfigList
	if err := c.List(context.Background(), &children); err != nil {
		t.Fatalf("list children: %v", err)
	}
	if len(children.Items) != 2 {
		t.Fatalf("got %d children, want 2", len(children.Items))
	}
	for _, child := range children.Items {
		if child.Labels["config.cisco.vk/bundle"] != "edges" {
			t.Errorf("child %s missing bundle label: %v", child.Name, child.Labels)
		}
		if child.Spec.DeviceRef.Name != "edge-01" && child.Spec.DeviceRef.Name != "edge-02" {
			t.Errorf("child targets unexpected device %q", child.Spec.DeviceRef.Name)
		}
	}
}

func TestBundleReconcilePrunesOrphans(t *testing.T) {
	// edge-01 used to match the selector but its label changed.
	// The pre-existing child must be deleted on the next
	// reconcile.
	scheme := bundleScheme(t)
	d1 := mkLabeledDevice("edge-01", "network", map[string]string{"role": "edge-was-here"}) // no longer matches
	d2 := mkLabeledDevice("edge-02", "network", map[string]string{"role": "edge"})
	b := mkBundle("edges", "network", map[string]string{"role": "edge"})
	stale := &configv1alpha1.IOSXEConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: "edges-edge-01", Namespace: "network",
			Labels: map[string]string{"config.cisco.vk/bundle": "edges"},
		},
		Spec: configv1alpha1.IOSXEConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "edge-01"},
			ManagedFamilies: []string{"vlan"},
			Source: configv1alpha1.ConfigurationSource{
				Inline: &runtime.RawExtension{Raw: []byte(`{}`)},
			},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(d1, d2, b, stale).
		WithStatusSubresource(&configv1alpha1.IOSXEConfigBundle{}).
		Build()
	r := &IOSXEConfigBundleReconciler{Client: c, Scheme: scheme}

	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "edges", Namespace: "network"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXEConfig
	err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "edges-edge-01"}, &got)
	if err == nil {
		t.Fatalf("stale child for edge-01 was not pruned")
	}
}

func TestBundleReconcileNoMatchingDevicesIsNotAnError(t *testing.T) {
	// Empty match set returns success but flips Ready=False with a
	// useful message — operators typically author against a not-
	// yet-deployed fleet, and a hard error here would mask that.
	scheme := bundleScheme(t)
	b := mkBundle("future", "network", map[string]string{"role": "doesnotexist"})
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithObjects(b).
		WithStatusSubresource(&configv1alpha1.IOSXEConfigBundle{}).
		Build()
	r := &IOSXEConfigBundleReconciler{Client: c, Scheme: scheme}
	if _, err := r.Reconcile(context.Background(), reconcile.Request{
		NamespacedName: types.NamespacedName{Name: "future", Namespace: "network"},
	}); err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.IOSXEConfigBundle
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "future"}, &got); err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status.MemberDevices != 0 {
		t.Errorf("MemberDevices=%d, want 0", got.Status.MemberDevices)
	}
	var ready *metav1.Condition
	for i := range got.Status.Conditions {
		if got.Status.Conditions[i].Type == "Ready" {
			ready = &got.Status.Conditions[i]
		}
	}
	if ready == nil || ready.Status != metav1.ConditionFalse {
		t.Errorf("Ready condition: %+v", ready)
	}
}
