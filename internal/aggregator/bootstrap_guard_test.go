// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package aggregator

import (
	"context"
	"errors"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type bootstrapUnavailableReader struct{ client.Reader }

func (r bootstrapUnavailableReader) Get(context.Context, client.ObjectKey, client.Object, ...client.GetOption) error {
	return errors.New("test uncached API unavailable")
}

func TestAggregatorBootstrapGuard(t *testing.T) {
	for _, mode := range []string{"unknown-held", "unknown-absent", "held", "absent", "retained-identity", "missing-reader", "read-error", "replaced-device", "source-change", "disabled"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			scheme := aggScheme(t)
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "tenant", UID: "device", Generation: 1}, Spec: cvk.DeviceSpec{PhysicalIdentity: "BOOTSTRAP123"}}
			if mode == "unknown-held" || mode == "unknown-absent" {
				device.Spec.PhysicalIdentity = ""
			}
			if mode == "retained-identity" {
				device.Spec.PhysicalIdentity = ""
				device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{PhysicalIdentity: "bootstrap123"}
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device).Build()
			if err := c.Get(ctx, client.ObjectKeyFromObject(device), device); err != nil {
				t.Fatal(err)
			}
			r := &AggregatedReconciler{Client: c, APIReader: c, BootstrapFenceNamespace: "leases"}
			if mode == "missing-reader" {
				r.APIReader = nil
			}
			if mode == "read-error" {
				r.APIReader = bootstrapUnavailableReader{Reader: c}
			}
			if mode == "disabled" {
				r.BootstrapFenceNamespace = ""
			}
			guard := r.bootstrapMutationGuard(device)
			if mode == "disabled" {
				if guard != nil {
					t.Fatal("default guard changed")
				}
				return
			}
			// Mutating caller memory must not retarget an already-created guard.
			device.Spec.PhysicalIdentity = "caller-mutated"
			if mode == "unknown-held" || mode == "held" || mode == "retained-identity" {
				name, _ := contract.BootstrapFenceName("bootstrap123")
				if err := c.Create(ctx, &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "leases"}}); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "source-change" || mode == "replaced-device" {
				var current cvk.CiscoDevice
				if err := c.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
					t.Fatal(err)
				}
				if mode == "source-change" {
					current.Spec.PhysicalIdentity = "changed"
					if err := c.Update(ctx, &current); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := c.Delete(ctx, &current); err != nil {
						t.Fatal(err)
					}
					current.UID = "replacement"
					current.ResourceVersion = ""
					if err := c.Create(ctx, &current); err != nil {
						t.Fatal(err)
					}
				}
			}
			_, finish, err := guard(ctx)
			if (err == nil) != (mode == "absent" || mode == "unknown-absent") {
				t.Fatal("bootstrap mutation guard outcome", err)
			}
			if err != nil {
				if finish != nil {
					t.Fatal("failed guard returned completion")
				}
				return
			}
			finish(errors.New("unknown outcome")) // Must not clear any later evidence.
			name, _ := contract.BootstrapFenceName("bootstrap123")
			if err := c.Create(ctx, &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "leases"}}); err != nil {
				t.Fatal(err)
			}
			if _, _, err := guard(ctx); err == nil {
				t.Fatal("existing worker reused stale absence")
			}
		})
	}
}

func TestAggregatorBootstrapHoldCancelsExistingWorker(t *testing.T) {
	registerStubFakeDriver(t)
	ctx := context.Background()
	scheme := aggScheme(t)
	device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "tenant", UID: "device", Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverFAKE, PhysicalIdentity: "BOOTSTRAP123"}, Status: cvk.DeviceStatus{Conditions: []metav1.Condition{{Type: cvk.CiscoDeviceConditionAggregatorOwned, Status: metav1.ConditionTrue}}}}
	name, _ := contract.BootstrapFenceName("bootstrap123")
	lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "leases"}}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(device, lease).Build()
	cancelled := false
	r := &AggregatedReconciler{Client: c, APIReader: c, BootstrapFenceNamespace: "leases", managed: map[string]*deviceWorker{devKey(device): {cancel: func() { cancelled = true }, specHash: "old"}}}
	result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(device)})
	if err == nil || !cancelled || len(r.managed) != 0 || result.RequeueAfter <= 0 {
		t.Fatal("held aggregator worker was not cancelled", err, result)
	}
	var retained coordination.Lease
	if err := c.Get(ctx, client.ObjectKeyFromObject(lease), &retained); err != nil {
		t.Fatal("guard removed bootstrap evidence", err)
	}
}
