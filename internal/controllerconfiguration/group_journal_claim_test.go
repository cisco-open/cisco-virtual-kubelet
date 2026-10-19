// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestClaimJournalGroupForController(t *testing.T) {
	for _, mode := range []string{"valid", "missing-member", "denied", "revoked", "prior-claim"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			if mode == "missing-member" {
				if err := c.Delete(ctx, members[0]); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "revoked" {
				members[0].Status.Grant.Phase = "Revoked"
				if err := c.Status().Update(ctx, members[0]); err != nil {
					t.Fatal(err)
				}
			}
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("installation denied")
				}
				return nil
			}
			if mode == "prior-claim" {
				if _, err := ClaimJournalGroupForController(ctx, c, c, nc, "leases", authorize, now); err != nil {
					t.Fatal(err)
				}
			}
			permit, err := ClaimJournalGroupForController(ctx, c, c, nc, "leases", authorize, now)
			if (err == nil) != (mode == "valid") || (permit != nil) != (mode == "valid") {
				t.Fatalf("journal claim: permit=%v error=%v", permit != nil, err)
			}
			if mode == "valid" {
				if _, err := ClaimJournalGroupForController(ctx, c, c, nc, "leases", authorize, now); err == nil {
					t.Fatal("restart reconstructed claim authority")
				}
			}
			for _, member := range members {
				var current ops.ControllerConfigurationReservation
				if err := c.Get(ctx, client.ObjectKeyFromObject(member), &current); err != nil {
					if mode == "missing-member" && member.Name == members[0].Name {
						continue
					}
					t.Fatal(err)
				}
				if (current.Status.Claim != nil) != (mode == "valid" || mode == "prior-claim") {
					t.Fatal("unexpected retained claim")
				}
				if current.Status.Completion != nil {
					t.Fatal("claim published completion")
				}
			}
		})
	}
}
