// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestCancelledGroupUnstartedSettlement(t *testing.T) {
	for _, mode := range []string{"idle", "absent-lease", "held", "foreign-holder", "guarded", "wrong-node", "wrong-device", "missing-identity", "granted", "denied", "lost-response", "late-node", "late-lease", "late-device"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupCancelledManagerFixture(t)
			row := members[0]
			leaseName := row.Status.Grant.Lease.Name
			holder := row.Status.Grant.Holder
			row.Status.Grant = nil
			row.Status.Claim = nil
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			must(c.Status().Update(ctx, row))
			var d cvk.CiscoDevice
			must(c.Get(ctx, client.ObjectKey{Namespace: row.Namespace, Name: row.Spec.Device.Name}, &d))
			d.Status.MaintenanceSession = nil
			if mode == "missing-identity" {
				d.Status.NodeIdentity = nil
			}
			must(c.Status().Update(ctx, &d))
			if mode == "wrong-device" {
				row.Spec.Device.UID = "other"
				must(c.Update(ctx, row))
			}
			var node core.Node
			must(c.Get(ctx, client.ObjectKey{Name: row.Spec.Node.Name}, &node))
			if mode != "guarded" {
				node.Spec.Taints = nil
			}
			if mode == "wrong-node" {
				node.Annotations = nil
			}
			must(c.Update(ctx, &node))
			var lease coordination.Lease
			must(c.Get(ctx, client.ObjectKey{Namespace: "leases", Name: leaseName}, &lease))
			switch mode {
			case "absent-lease":
				must(c.Delete(ctx, &lease))
			case "held", "foreign-holder":
				if mode == "foreign-holder" {
					holder = "other"
				}
				lease.Spec.HolderIdentity = &holder
				must(c.Update(ctx, &lease))
			case "granted":
				row.Status.Grant = members[1].Status.Grant.DeepCopy()
				must(c.Status().Update(ctx, row))
			}
			reader := client.Reader(c)
			if mode == "late-node" || mode == "late-lease" || mode == "late-device" {
				reader = &unstartedChangingReader{Reader: c, mode: mode}
			}
			auth := func(context.Context, *ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("denied")
				}
				return nil
			}
			writer := client.StatusClient(c)
			if mode == "lost-response" {
				writer = ambiguousStatus{Client: c}
			}
			err := PublishCancelledGroupManagerSettlement(ctx, reader, writer, nc, row, "leases", auth, now)
			valid := mode == "idle" || mode == "absent-lease"
			if (err == nil) != valid {
				t.Fatalf("mode=%s error=%v", mode, err)
			}
			must(c.Get(ctx, client.ObjectKeyFromObject(row), row))
			if (row.Status.ManagerSettledAt != nil) != (valid || mode == "lost-response") {
				t.Fatal("incorrect retained settlement")
			}
			if valid || mode == "lost-response" {
				must(PublishCancelledGroupManagerSettlement(ctx, c, c, nc, row, "leases", auth, now))
			}
			must(c.Get(ctx, client.ObjectKeyFromObject(&d), &d))
			if d.Status.MaintenanceSession != nil || row.Status.Completion != nil {
				t.Fatal("fabricated session or completion")
			}
		})
	}
}

type unstartedChangingReader struct {
	client.Reader
	mode  string
	reads int
}

func (r *unstartedChangingReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if err := r.Reader.Get(ctx, key, obj, opts...); err != nil {
		return err
	}
	match := false
	switch obj.(type) {
	case *core.Node:
		match = r.mode == "late-node"
	case *coordination.Lease:
		match = r.mode == "late-lease"
	case *cvk.CiscoDevice:
		match = r.mode == "late-device"
	}
	if match {
		r.reads++
		if r.reads == 2 {
			obj.SetResourceVersion("changed")
		}
	}
	return nil
}
