// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestSingleClaimRetentionDoesNotReauthorizeDispatch(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown", "verifying", "paused-new-generation", "old-grant-renewed", "wrong-claim", "future-claim", "stale-reservation", "foreign-lease", "expired-lease", "settled"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			r := members[0].DeepCopy()
			op := nc.Status.Configuration.Operation
			op.PhysicalGroup = nil
			op.TargetID, op.TargetSerial = r.Spec.InventoryID, r.Spec.PhysicalIdentity
			op.Resource, op.Phase, op.TaskID = "management-address:retained-example", "Accepted", "task"
			submitted := metav1.NewTime(now.Add(-5 * time.Second))
			op.SubmittedAt = &submitted
			if mode == "unknown" {
				op.Phase = "Unknown"
			}
			if mode == "verifying" {
				op.Phase = "Verifying"
			}
			digest, err := OperationDigest(op)
			if err != nil {
				t.Fatal(err)
			}
			r.Spec.Protocol, r.Spec.OperationDigest = Protocol, digest
			r.Name, err = ReservationName(string(nc.UID), op.ID)
			if err != nil {
				t.Fatal(err)
			}
			r.UID, r.ResourceVersion = "single-reservation", ""
			r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: digest, ClaimedAt: metav1.NewTime(now.Add(-10 * time.Second))}
			holder, err := Holder(r.Namespace, r)
			if err != nil {
				t.Fatal(err)
			}
			r.Status.Grant.Holder = holder
			var lease coordination.Lease
			if err := c.Get(ctx, client.ObjectKey{Namespace: "leases", Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
				t.Fatal(err)
			}
			lease.Spec.HolderIdentity = &holder
			switch mode {
			case "wrong-claim":
				r.Status.Claim.OperationID = "foreign"
			case "future-claim":
				r.Status.Claim.ClaimedAt = metav1.NewTime(now.Add(time.Hour))
			case "settled":
				r.Status.ManagerSettledAt = &submitted
			case "expired-lease":
				lease.Spec.RenewTime = &metav1.MicroTime{Time: now.Add(-time.Hour)}
			case "old-grant-renewed":
				r.Status.Grant.GrantedAt = metav1.NewTime(now.Add(-time.Hour))
				lease.Spec.AcquireTime = &metav1.MicroTime{Time: now.Add(-time.Hour)}
			}
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if mode == "paused-new-generation" {
				nc.Generation++
				nc.Spec.Paused = true
				if err := c.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Update(ctx, &lease); err != nil {
				t.Fatal(err)
			}
			if err := c.Create(ctx, r); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(&lease), &lease); err != nil {
				t.Fatal(err)
			}
			if mode == "foreign-lease" {
				lease.UID = "foreign"
			}
			if mode == "stale-reservation" {
				r.ResourceVersion = "stale"
			}
			err = ReadClaimRetention(ctx, c, r, &lease, "leases", now)
			valid := mode == "accepted" || mode == "unknown" || mode == "verifying" || mode == "paused-new-generation" || mode == "old-grant-renewed"
			if (err == nil) != valid {
				t.Fatalf("retention mode=%s error=%v", mode, err)
			}
		})
	}
}
