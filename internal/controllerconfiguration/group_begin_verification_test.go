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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestBeginGroupVerification(t *testing.T) {
	for _, mode := range []string{"valid", "unknown", "submitting", "missing-task", "missing-owner", "missing-claim", "released", "subset", "denied", "late-member", "late-controller"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			op := nc.Status.Configuration.Operation
			op.Phase, op.TaskID = "Accepted", "task-id"
			at := metav1.NewTime(now.Add(-time.Second))
			op.SubmittedAt = &at
			nc.Status.Configuration.Owners = []cvk.NetworkControllerSectionOwner{{Section: op.Section, ConfigName: op.ConfigName, ConfigUID: op.ConfigUID}}
			for _, r := range members {
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, ClaimedAt: metav1.NewTime(now.Add(-2 * time.Second))}
			}
			switch mode {
			case "unknown":
				op.Phase = "Unknown"
			case "submitting":
				op.Phase = "Submitting"
			case "missing-task":
				op.TaskID = ""
			case "missing-owner":
				nc.Status.Configuration.Owners = nil
			case "missing-claim":
				members[0].Status.Claim = nil
			case "released":
				members[0].Status.Grant.Phase = "Released"
			}
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			for _, r := range members {
				if err := c.Status().Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "subset" {
				members = members[:1]
			}
			observe := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("native evidence incomplete")
				}
				if mode == "late-member" {
					r := members[0].DeepCopy()
					r.Status.Grant.Phase = "Revoked"
					return c.Status().Update(ctx, r)
				}
				if mode == "late-controller" {
					copy := nc.DeepCopy()
					copy.Status.Configuration.Operation.TaskID = "other"
					return c.Status().Update(ctx, copy)
				}
				return nil
			}
			err := BeginGroupVerification(ctx, c, c, nc, members, observe, now)
			valid := mode == "valid" || mode == "unknown"
			if (err == nil) != valid {
				t.Fatal("verification transition", err)
			}
			var stored cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &stored); err != nil {
				t.Fatal(err)
			}
			if (stored.Status.Configuration.Operation.Phase == "Verifying") != valid || len(stored.Status.Configuration.ActionApplications) != 0 {
				t.Fatal("unexpected verification/receipt")
			}
			for _, r := range members {
				var fresh ops.ControllerConfigurationReservation
				if err := c.Get(ctx, client.ObjectKeyFromObject(r), &fresh); err != nil {
					t.Fatal(err)
				}
				if fresh.Status.Completion != nil || fresh.Status.ManagerSettledAt != nil {
					t.Fatal("premature release")
				}
			}
		})
	}
}
