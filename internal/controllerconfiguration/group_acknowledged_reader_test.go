// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestReadAcknowledgedGroupReservations(t *testing.T) {
	for _, mode := range []string{"accepted", "unknown", "verifying", "prepared", "submitting", "verified", "missing-task", "bad-task", "future-submission", "missing-owner", "duplicate-owner", "missing-member", "missing-claim", "late-claim", "released", "settled", "paused", "new-generation", "stale-controller"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			op := nc.Status.Configuration.Operation
			op.Phase, op.TaskID = "Accepted", "task-id"
			at := metav1.NewTime(now.Add(-time.Second))
			op.SubmittedAt = &at
			nc.Status.Configuration.Owners = []cvk.NetworkControllerSectionOwner{{Section: op.Section, ConfigName: op.ConfigName, ConfigUID: op.ConfigUID}}
			for _, member := range members {
				member.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, ClaimedAt: metav1.NewTime(now.Add(-2 * time.Second))}
			}
			switch mode {
			case "unknown":
				op.Phase = "Unknown"
			case "verifying":
				op.Phase = "Verifying"
			case "prepared":
				op.Phase = "Prepared"
			case "submitting":
				op.Phase = "Submitting"
			case "verified":
				op.Phase = "Verified"
			case "missing-task":
				op.TaskID = ""
			case "bad-task":
				op.TaskID = "task/other"
			case "future-submission":
				op.SubmittedAt = &metav1.Time{Time: now.Add(time.Minute)}
			case "missing-owner":
				nc.Status.Configuration.Owners = nil
			case "duplicate-owner":
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, nc.Status.Configuration.Owners[0])
			case "missing-claim":
				members[0].Status.Claim = nil
			case "late-claim":
				members[0].Status.Claim.ClaimedAt = metav1.NewTime(now)
			case "released":
				members[0].Status.Grant.Phase = "Released"
			case "settled":
				members[0].Status.ManagerSettledAt = &metav1.Time{Time: now}
			case "paused":
				nc.Spec.Paused = true
			case "new-generation":
				nc.Generation++
			}
			desiredStatus := nc.DeepCopy().Status
			if err := c.Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			nc.Status = desiredStatus
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			for _, member := range members {
				if err := c.Status().Update(ctx, member); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing-member" {
				if err := c.Delete(ctx, members[0]); err != nil {
					t.Fatal(err)
				}
			}
			expected := nc.DeepCopy()
			if mode == "stale-controller" {
				nc.Status.Configuration.Operation.TaskID = "different-task"
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			var before cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &before); err != nil {
				t.Fatal(err)
			}
			got, err := ReadAcknowledgedGroupReservations(ctx, c, expected, now)
			valid := mode == "accepted" || mode == "unknown" || mode == "verifying"
			if (err == nil) != valid {
				t.Fatalf("read recovery members: %v", err)
			}
			if valid && len(got) != len(members) {
				t.Fatal("incomplete recovery member set")
			}
			if !valid && got != nil {
				t.Fatal("partial member set escaped")
			}
			var after cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("reader changed journal")
			}
		})
	}
}
