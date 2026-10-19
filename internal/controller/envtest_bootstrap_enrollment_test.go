//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

// Actual API identity/defaults and retained objects; Reconcile must refuse
// before finalizer, teardown, projection or worker writes in legacy topology.
// No device API, production manager, or native admission-policy rollout occurs.
func TestEnvtest_BootstrapLegacyEnrollmentBarrier(t *testing.T) {
	environment := &envtest.Environment{CRDDirectoryPaths: []string{"../../config/crd/cisco.vk_ciscodevices.yaml"}, ErrorIfCRDPathMissing: true}
	cfg, err := environment.Start()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := environment.Stop(); err != nil {
			t.Error(err)
		}
	})
	c, err := client.New(cfg, client.Options{Scheme: drainTestScheme(t)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	for _, name := range []string{"tenant", "leases"} {
		if err := c.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}}); err != nil {
			t.Fatal(err)
		}
	}
	for _, identity := range []string{"known", "unknown"} {
		t.Run(identity, func(t *testing.T) {
			d := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Name: identity, Namespace: "tenant", Finalizers: []string{ciscoDeviceFinalizer}}, Spec: cvk.DeviceSpec{Address: "198.51.100.101", Driver: cvk.DeviceDriverXE, Username: "fixture"}}
			if identity == "known" {
				d.Spec.PhysicalIdentity = "SERIAL123"
			}
			if err := c.Create(ctx, d); err != nil {
				t.Fatal(err)
			}
			writes := &bootstrapEnrollmentWrites{Client: c}
			r := &CiscoDeviceReconciler{Client: writes, APIReader: c, Scheme: c.Scheme(), ManagedTopology: false, BootstrapFenceNamespace: "leases"}
			if err := r.ensureBootstrapEnrollmentClear(ctx, d); err != nil {
				t.Fatal("clear namespace blocked legacy consumer", err)
			}
			name, _ := contract.BootstrapFenceName("serial123")
			fence := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "leases", Annotations: map[string]string{"ops.cisco.vk/metadata-fence-release": "retained-release-marker"}}}
			if err := c.Create(ctx, fence); err != nil {
				t.Fatal(err)
			}
			check := func() {
				t.Helper()
				before := d.DeepCopy()
				result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(d)})
				if err == nil || result.RequeueAfter <= 0 || writes.writes != 0 {
					t.Fatal("legacy fence barrier allowed enrollment/cleanup writes", err, result, writes.writes)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(d), d); err != nil {
					t.Fatal(err)
				}
				if d.ResourceVersion != before.ResourceVersion || len(d.Finalizers) != len(before.Finalizers) {
					t.Fatal("held device was changed")
				}
				var retained coordination.Lease
				if err := c.Get(ctx, client.ObjectKeyFromObject(fence), &retained); err != nil || retained.UID != fence.UID {
					t.Fatal("consumer erased/replaced retained custody", err)
				}
			}
			check()
			if err := c.Delete(ctx, d); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(d), d); err != nil {
				t.Fatal(err)
			}
			check()
			if err := c.Delete(ctx, fence); err != nil {
				t.Fatal(err)
			}
			if err := r.ensureBootstrapEnrollmentClear(ctx, d); err != nil {
				t.Fatal("verified fence absence did not release enrollment hold", err)
			}
			if writes.writes != 0 {
				t.Fatal("read-only barriers wrote state")
			}
		})
	}
}
