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
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func groupCancelledManagerFixture(t *testing.T) (client.Client, *cvk.NetworkController, []*ops.ControllerConfigurationReservation, time.Time) {
	t.Helper()
	ctx := context.Background()
	c, nc, members, now := groupAuthorityFixture(t)
	op := nc.Status.Configuration.Operation
	op.Phase = "Cancelling"
	at := metav1.NewTime(now.Add(-time.Second))
	op.CancellationRequestedAt = &at
	if err := c.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	for _, r := range members {
		r.Status.Grant.Phase = "Released"
		r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now.Add(-10 * time.Second))}
	}
	for _, r := range members {
		r.Status.GroupCancellation = &ops.ConfigurationReservationGroupCancellation{OperationID: op.ID, OperationDigest: r.Spec.OperationDigest, RequestedAt: at, AcknowledgedAt: metav1.NewTime(now)}
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

func TestCancelledGroupManagerSettlement(t *testing.T) {
	for _, mode := range []string{"valid", "active-session", "wrong-session", "no-session", "wrong-lease", "held-lease", "ungranted", "ungranted-held", "unreleased", "no-ack", "claimed", "future-marker", "early-marker", "denied", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupCancelledManagerFixture(t)
			target := members[0]
			switch mode {
			case "active-session", "wrong-session", "no-session", "wrong-lease":
				var d cvk.CiscoDevice
				if err := c.Get(ctx, client.ObjectKey{Namespace: target.Namespace, Name: target.Spec.Device.Name}, &d); err != nil {
					t.Fatal(err)
				}
				if mode == "active-session" {
					d.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionActive
				} else if mode == "no-session" {
					d.Status.MaintenanceSession = nil
				} else if mode == "wrong-lease" {
					d.Status.MaintenanceSession.Lease.UID = "other"
				} else {
					d.Status.MaintenanceSession.Operation.UID = "other"
				}
				if err := c.Status().Update(ctx, &d); err != nil {
					t.Fatal(err)
				}
			case "held-lease", "ungranted-held":
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
			switch mode {
			case "ungranted", "ungranted-held":
				target.Status.Grant = nil
				target.Status.Claim = nil
			case "unreleased":
				target.Status.Grant.Phase = "Held"
			case "no-ack":
				target.Status.GroupCancellation = nil
			}
			if err := c.Status().Update(ctx, target); err != nil {
				t.Fatal(err)
			}
			original := target.DeepCopy()
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
			err := PublishCancelledGroupManagerSettlement(ctx, c, writer, nc, target, "leases", authorize, now)
			valid := mode == "valid" || mode == "ungranted" || mode == "claimed"
			if (err == nil) != valid {
				t.Fatal(mode, err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil {
				t.Fatal(err)
			}
			if valid || mode == "lost-response" {
				if target.Status.ManagerSettledAt == nil {
					t.Fatal("manager evidence lost")
				}
				rv := target.ResourceVersion
				if err := PublishCancelledGroupManagerSettlement(ctx, c, c, nc, target, "leases", authorize, now); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(target), target); err != nil {
					t.Fatal(err)
				}
				if target.ResourceVersion != rv {
					t.Fatal("repeated settlement rewrote evidence")
				}
				if err := ValidateCancelledGroupMemberSettlement(nc, members[1], now); err == nil {
					t.Fatal("peer incorrectly settled")
				}
				original.Status.ManagerSettledAt = target.Status.ManagerSettledAt
			}
			if !equality.Semantic.DeepEqual(original.Status, target.Status) {
				t.Fatal("settlement altered other evidence")
			}
		})
	}
}

// Exercise the ordering of the cancellation primitives. These fixture writes
// simulate the worker/manager cleanup; the helpers must not perform that cleanup
// themselves or treat acknowledgement as release/settlement authority.
func TestCancelledGroupCleanupOrdering(t *testing.T) {
	ctx := context.Background()
	c, nc, members, now := groupAuthorityFixture(t)
	at := metav1.NewTime(now)
	nc.Status.Configuration.Operation.Phase = "Cancelling"
	nc.Status.Configuration.Operation.CancellationRequestedAt = &at
	if err := c.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	authorize := func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
	for _, r := range members {
		if err := AcknowledgeGroupCancellation(ctx, c, c, nc, r, r.Spec.DeviceWorkerPod.UID, authorize, now); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
			t.Fatal(err)
		}
		if err := MarkCancelledGroupMemberReleased(ctx, c, c, nc, r, r.Spec.DeviceWorkerPod.UID, "leases", authorize, now); err == nil {
			t.Fatal("acknowledgement bypassed lease ownership")
		}
		var lease coordination.Lease
		if err := c.Get(ctx, client.ObjectKey{Namespace: "leases", Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
			t.Fatal(err)
		}
		lease.Spec.HolderIdentity = nil
		if err := c.Update(ctx, &lease); err != nil {
			t.Fatal(err)
		}
		if err := MarkCancelledGroupMemberReleased(ctx, c, c, nc, r, r.Spec.DeviceWorkerPod.UID, "leases", authorize, now); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
			t.Fatal(err)
		}
		if err := PublishCancelledGroupManagerSettlement(ctx, c, c, nc, r, "leases", authorize, now); err == nil {
			t.Fatal("release bypassed active maintenance session")
		}
		var device cvk.CiscoDevice
		if err := c.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}, &device); err != nil {
			t.Fatal(err)
		}
		device.Status.MaintenanceSession.Phase = cvk.DeviceMaintenanceSessionSettled
		if err := c.Status().Update(ctx, &device); err != nil {
			t.Fatal(err)
		}
		if err := PublishCancelledGroupManagerSettlement(ctx, c, c, nc, r, "leases", authorize, now); err != nil {
			t.Fatal(err)
		}
		if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
			t.Fatal(err)
		}
		if err := ValidateCancelledGroupMemberSettlement(nc, r, now); err != nil {
			t.Fatal(err)
		}
		if r.Status.Completion != nil {
			t.Fatal("cancellation fabricated successful completion")
		}
	}
	var current cvk.NetworkController
	if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &current); err != nil {
		t.Fatal(err)
	}
	if !equality.Semantic.DeepEqual(nc.Status, current.Status) {
		t.Fatal("cleanup helpers erased journal or changed receipts")
	}
}
