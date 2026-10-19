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

func TestGroupClaimRetention(t *testing.T) {
	for _, mode := range []string{"valid", "prepared-partial-claim", "paused-new-generation", "wrong-claim", "future-claim", "stale-member", "foreign-lease", "changed-peer", "missing-group"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			r := members[0]
			op := nc.Status.Configuration.Operation
			if mode != "prepared-partial-claim" {
				op.Phase = "Accepted"
				op.TaskID = "task"
				at := metav1.NewTime(now.Add(-5 * time.Second))
				op.SubmittedAt = &at
			}
			r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, ClaimedAt: metav1.NewTime(now.Add(-10 * time.Second))}
			if mode == "wrong-claim" {
				r.Status.Claim.OperationID = "foreign"
			}
			if mode == "future-claim" {
				r.Status.Claim.ClaimedAt = metav1.NewTime(now.Add(time.Hour))
			}
			if mode == "missing-group" {
				op.PhysicalGroup = nil
			}
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if mode == "paused-new-generation" {
				nc.Spec.Paused = true
				nc.Generation++
				if err := c.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Status().Update(ctx, r); err != nil {
				t.Fatal(err)
			}
			if mode == "stale-member" {
				r.ResourceVersion = "stale"
			}
			if mode == "changed-peer" {
				members[1].Spec.InventoryID = "foreign"
				if err := c.Update(ctx, members[1]); err != nil {
					t.Fatal(err)
				}
			}
			var lease coordination.Lease
			if err := c.Get(ctx, client.ObjectKey{Namespace: "leases", Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
				t.Fatal(err)
			}
			if mode == "foreign-lease" {
				lease.UID = "foreign"
			}
			err := ReadGroupClaimRetention(ctx, c, r, &lease, "leases", now)
			valid := mode == "valid" || mode == "prepared-partial-claim" || mode == "paused-new-generation"
			if (err == nil) != valid {
				t.Fatal("retention", err)
			}
		})
	}
}
