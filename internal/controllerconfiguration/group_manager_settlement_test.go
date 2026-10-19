// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func groupManagerFixture(t *testing.T) (client.Client, *cvk.NetworkController, []*ops.ControllerConfigurationReservation, time.Time) {
	t.Helper()
	ctx := context.Background()
	c, nc, members, now := groupAuthorityFixture(t)
	op := nc.Status.Configuration.Operation
	op.Phase = "Verified"
	op.TaskID = "task"
	submitted, verified := metav1.NewTime(now.Add(-9*time.Second)), metav1.NewTime(now.Add(-time.Second))
	op.SubmittedAt = &submitted
	nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: op.Section, Resource: "stable-resource", Operation: *op.DeepCopy(), VerifiedAt: &verified}}
	if err := c.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	for _, r := range members {
		r.Status.Grant.Phase = "Released"
		r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now.Add(-10 * time.Second))}
	}
	completions, err := GroupCompletionsForReceipt(nc, members, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range members {
		r.Status.Completion = completions[r.UID]
		if err := c.Status().Update(ctx, r); err != nil {
			t.Fatal(err)
		}
		var d cvk.CiscoDevice
		if err := c.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}, &d); err != nil {
			t.Fatal(err)
		}
		d.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionSettled
		if err := c.Status().Update(ctx, &d); err != nil {
			t.Fatal(err)
		}
		var lease coordination.Lease
		if err := c.Get(ctx, client.ObjectKey{Namespace: "leases", Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
			t.Fatal(err)
		}
		lease.Spec.HolderIdentity = nil
		if err := c.Update(ctx, &lease); err != nil {
			t.Fatal(err)
		}
	}
	return c, nc, members, now
}

func TestGroupManagerSettlement(t *testing.T) {
	for _, mode := range []string{"valid", "active-session", "wrong-session", "held-lease", "future-marker", "early-marker", "denied", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupManagerFixture(t)
			target := members[0]
			switch mode {
			case "active-session", "wrong-session":
				var d cvk.CiscoDevice
				if err := c.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: target.Spec.Device.Name}, &d); err != nil {
					t.Fatal(err)
				}
				if mode == "active-session" {
					d.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionActive
				} else {
					d.Status.MaintenanceSession.Operation.UID = "other"
				}
				if err := c.Status().Update(ctx, &d); err != nil {
					t.Fatal(err)
				}
			case "held-lease":
				var lease coordination.Lease
				if err := c.Get(ctx, client.ObjectKey{Namespace: "leases", Name: target.Status.Grant.Lease.Name}, &lease); err != nil {
					t.Fatal(err)
				}
				holder := target.Status.Grant.Holder
				lease.Spec.HolderIdentity = &holder
				if err := c.Update(ctx, &lease); err != nil {
					t.Fatal(err)
				}
			case "future-marker", "early-marker":
				at := metav1.NewTime(now.Add(time.Second))
				if mode == "early-marker" {
					at = metav1.NewTime(now.Add(-time.Minute))
				}
				target.Status.ManagerSettledAt = &at
				if err := c.Status().Update(ctx, target); err != nil {
					t.Fatal(err)
				}
			}
			authorize := func(context.Context, *ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("denied")
				}
				return nil
			}
			writer := client.StatusClient(c)
			if mode == "lost-response" {
				writer = ambiguousStatus{Client: c}
			}
			err := PublishGroupManagerSettlement(ctx, c, writer, nc, members, target, "leases", authorize, now)
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil {
				t.Fatal(err)
			}
			if mode == "valid" || mode == "lost-response" {
				if target.Status.ManagerSettledAt == nil {
					t.Fatal("manager evidence lost")
				}
				if err := ValidateGroupManagerSettlement(nc, members, now); err == nil {
					t.Fatal("one settled member settled entire group")
				}
				for _, r := range members {
					if err := PublishGroupManagerSettlement(ctx, c, c, nc, members, r, "leases", authorize, now); err != nil {
						t.Fatal(err)
					}
					if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
						t.Fatal(err)
					}
				}
				if err := ValidateGroupManagerSettlement(nc, members, now); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
