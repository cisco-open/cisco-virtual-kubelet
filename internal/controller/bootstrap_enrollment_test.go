// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type bootstrapEnrollmentReader struct {
	client.Reader
	mode        string
	deviceReads int
}

func (r *bootstrapEnrollmentReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*coordination.Lease); ok && r.mode == "unavailable-api" {
		return errors.New("test API unavailable")
	}
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	if device, ok := obj.(*cvk.CiscoDevice); ok {
		r.deviceReads++
		if r.mode == "late-device" && r.deviceReads > 1 {
			device.ResourceVersion = "changed"
		}
		if r.mode == "stale-device" {
			device.Spec.PhysicalIdentity = "changed"
		}
	}
	return nil
}

type bootstrapEnrollmentWrites struct {
	client.Client
	writes int
}

func (c *bootstrapEnrollmentWrites) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	c.writes++
	return errors.New("unexpected create")
}
func (c *bootstrapEnrollmentWrites) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	c.writes++
	return errors.New("unexpected update")
}
func (c *bootstrapEnrollmentWrites) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	c.writes++
	return errors.New("unexpected patch")
}
func (c *bootstrapEnrollmentWrites) Delete(ctx context.Context, obj client.Object, opts ...client.DeleteOption) error {
	c.writes++
	return errors.New("unexpected delete")
}

func bootstrapEnrollmentFixture(t *testing.T, mode string) (*CiscoDeviceReconciler, *cvk.CiscoDevice, *bootstrapEnrollmentWrites) {
	t.Helper()
	device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: "edge", Namespace: "tenant", UID: "device", Generation: 1}, Spec: cvk.DeviceSpec{PhysicalIdentity: "BOOTSTRAP123", Address: "198.51.100.101", Driver: "iosxe"}}
	if mode == "legacy-unknown-held" || mode == "legacy-unknown-absent" {
		device.Spec.PhysicalIdentity = ""
	}
	if mode == "retained-identity" {
		device.Spec.PhysicalIdentity = ""
		device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{PhysicalIdentity: "bootstrap123"}
	}
	if mode == "deleting-device" {
		now := metav1.Now()
		device.DeletionTimestamp = &now
		device.Finalizers = []string{ciscoDeviceFinalizer}
	}
	name, err := contract.BootstrapFenceName("bootstrap123")
	if err != nil {
		t.Fatal(err)
	}
	lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "leases", UID: "retained"}}
	if mode == "other-serial" {
		lease.Name, err = contract.BootstrapFenceName("other")
		if err != nil {
			t.Fatal(err)
		}
	}
	if mode == "other-namespace" {
		lease.Namespace = device.Namespace
	}
	if mode == "expired" {
		ttl := int32(1)
		lease.Spec.LeaseDurationSeconds = &ttl
	}
	if mode == "foreign-holder" {
		holder := "another-operation"
		lease.Spec.HolderIdentity = &holder
	}
	if mode == "deleting-fence" {
		now := metav1.Now()
		lease.DeletionTimestamp = &now
		lease.Finalizers = []string{"retain-test-evidence"}
	}
	scheme := runtime.NewScheme()
	_ = cvk.AddToScheme(scheme)
	_ = coordination.AddToScheme(scheme)
	objects := []client.Object{device}
	if mode != "legacy-absent" && mode != "legacy-unknown-absent" && mode != "absent" && mode != "late-device" && mode != "stale-device" && mode != "unavailable-api" && mode != "missing-api-reader" {
		objects = append(objects, lease)
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(device), device); err != nil {
		t.Fatal(err)
	}
	writes := &bootstrapEnrollmentWrites{Client: c}
	r := &CiscoDeviceReconciler{Client: writes, APIReader: &bootstrapEnrollmentReader{Reader: c, mode: mode}, Scheme: scheme, ManagedTopology: true, BootstrapFenceNamespace: "leases"}
	if mode == "missing-api-reader" {
		r.APIReader = nil
	}
	if mode == "disabled" {
		r.BootstrapFenceNamespace = ""
	}
	if strings.HasPrefix(mode, "legacy-") {
		r.ManagedTopology = false
	}
	return r, device, writes
}

func TestBootstrapEnrollmentGuard(t *testing.T) {
	for _, mode := range []string{"absent", "held", "expired", "foreign-holder", "deleting-fence", "other-serial", "other-namespace", "retained-identity", "missing-api-reader", "late-device", "stale-device", "unavailable-api", "disabled", "legacy-held", "legacy-absent", "legacy-unknown-held", "legacy-unknown-absent"} {
		t.Run(mode, func(t *testing.T) {
			r, device, writes := bootstrapEnrollmentFixture(t, mode)
			err := r.ensureBootstrapEnrollmentClear(context.Background(), device)
			allowed := mode == "absent" || mode == "other-serial" || mode == "other-namespace" || mode == "disabled" || mode == "legacy-absent" || mode == "legacy-unknown-absent"
			if (err == nil) != allowed {
				t.Fatal("enrollment guard outcome", err)
			}
			if writes.writes != 0 {
				t.Fatal("read-only enrollment guard wrote state")
			}
		})
	}
}

func TestBootstrapEnrollmentReconcileBarrier(t *testing.T) {
	for _, mode := range []string{"legacy-held", "legacy-unknown-held", "held", "expired", "foreign-holder", "deleting-fence", "deleting-device", "retained-identity", "missing-api-reader", "stale-device", "late-device", "unavailable-api"} {
		t.Run(mode, func(t *testing.T) {
			r, device, writes := bootstrapEnrollmentFixture(t, mode)
			result, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(device)})
			if err == nil || result.RequeueAfter <= 0 || writes.writes != 0 {
				t.Fatal("bootstrap hold failed before finalizer/teardown/projection/worker writes", err, result, writes.writes)
			}
			var retained cvk.CiscoDevice
			if err := writes.Get(context.Background(), client.ObjectKeyFromObject(device), &retained); err != nil {
				t.Fatal(err)
			}
			if len(retained.Finalizers) != len(device.Finalizers) {
				t.Fatal("bootstrap hold altered device finalizers")
			}
		})
	}
}
