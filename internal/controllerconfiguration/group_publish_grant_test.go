// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPublishGroupGrant(t *testing.T) {
	for _, mode := range []string{"valid", "foreign-pod", "claimed-peer", "revoked-peer", "changed-peer", "already-granted", "submitted", "v1-entry", "missing-guard", "wrong-session", "active-workload"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			r := members[0]
			if mode != "already-granted" {
				r.Status.Grant = nil
				if err := c.Status().Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			pod := r.Spec.DeviceWorkerPod.UID
			switch mode {
			case "missing-guard":
				var node core.Node
				if err := c.Get(ctx, client.ObjectKey{Name: r.Spec.Node.Name}, &node); err != nil {
					t.Fatal(err)
				}
				node.Spec.Taints = nil
				if err := c.Update(ctx, &node); err != nil {
					t.Fatal(err)
				}
			case "wrong-session":
				var device cvk.CiscoDevice
				if err := c.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}, &device); err != nil {
					t.Fatal(err)
				}
				device.Status.MaintenanceSession.Operation.UID = "foreign"
				if err := c.Status().Update(ctx, &device); err != nil {
					t.Fatal(err)
				}
			case "active-workload":
				pod := &core.Pod{ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Name: "workload"}, Spec: core.PodSpec{NodeName: r.Spec.Node.Name}, Status: core.PodStatus{Phase: core.PodRunning}}
				if err := c.Create(ctx, pod); err != nil {
					t.Fatal(err)
				}
			case "foreign-pod":
				pod = "foreign"
			case "claimed-peer":
				members[1].Status.Claim = &ops.ConfigurationReservationClaim{OperationID: r.Spec.OperationID}
			case "revoked-peer":
				members[1].Status.Grant.Phase = "Revoked"
			case "changed-peer":
				members[1].Spec.InventoryID = "foreign"
				if err := c.Update(ctx, members[1]); err != nil {
					t.Fatal(err)
				}
			case "submitted":
				nc.Status.Configuration.Operation.Phase = "Submitting"
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "claimed-peer" || mode == "revoked-peer" {
				if err := c.Status().Update(ctx, members[1]); err != nil {
					t.Fatal(err)
				}
			}
			publish := PublishGroupGrant
			if mode == "v1-entry" {
				publish = PublishGrant
			}
			err := publish(ctx, c, c, r, "leases", pod, now)
			if (err == nil) != (mode == "valid") {
				t.Fatal("group grant result", err)
			}
			var stored ops.ControllerConfigurationReservation
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), &stored); err != nil {
				t.Fatal(err)
			}
			if (stored.Status.Grant != nil) != (mode == "valid" || mode == "already-granted") {
				t.Fatal("unexpected grant write")
			}
			if stored.Status.Claim != nil || stored.Status.Completion != nil {
				t.Fatal("grant authorized execution/completion")
			}
		})
	}
}

func TestReadGroupMemberPreparation(t *testing.T) {
	for _, mode := range []string{"valid", "foreign-controller", "stale-member", "claimed-peer", "missing-group", "v1"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			r := members[0]
			r.Status.Grant = nil
			if err := c.Status().Update(ctx, r); err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "foreign-controller":
				r.Spec.Controller.UID = "foreign"
			case "stale-member":
				r.ResourceVersion = "stale"
			case "claimed-peer":
				members[1].Status.Claim = &ops.ConfigurationReservationClaim{OperationID: "claimed"}
				if err := c.Status().Update(ctx, members[1]); err != nil {
					t.Fatal(err)
				}
			case "missing-group":
				nc.Status.Configuration.Operation.PhysicalGroup = nil
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "v1":
				r.Spec.Protocol = Protocol
			}
			if err := ReadGroupMemberPreparation(ctx, c, r, now); (err == nil) != (mode == "valid") {
				t.Fatal("group preparation", err)
			}
		})
	}
}

func TestReadGroupHeldMember(t *testing.T) {
	for _, mode := range []string{"valid", "expired-grant", "missing-grant", "claimed-peer", "changed-intent", "foreign-lease", "wrong-namespace", "stale-member"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, _, members, now := groupAuthorityFixture(t)
			r := members[0]
			namespace := "leases"
			switch mode {
			case "expired-grant":
				r.Status.Grant.GrantedAt.Time = now.Add(-ConfigurationGrantMaxAge)
				if err := c.Status().Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			case "missing-grant":
				r.Status.Grant = nil
			case "claimed-peer":
				members[1].Status.Claim = &ops.ConfigurationReservationClaim{OperationID: r.Spec.OperationID}
				if err := c.Status().Update(ctx, members[1]); err != nil {
					t.Fatal(err)
				}
			case "changed-intent":
				r.Spec.ConfigGeneration++
				if err := c.Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			case "foreign-lease":
				var lease coordination.Lease
				if err := c.Get(ctx, client.ObjectKey{Namespace: namespace, Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
					t.Fatal(err)
				}
				foreign := "foreign"
				lease.Spec.HolderIdentity = &foreign
				if err := c.Update(ctx, &lease); err != nil {
					t.Fatal(err)
				}
			case "wrong-namespace":
				namespace = "foreign"
			case "stale-member":
				r.ResourceVersion = "stale"
			}
			if err := ReadGroupHeldMember(ctx, c, r, namespace, now); (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
		})
	}
}
