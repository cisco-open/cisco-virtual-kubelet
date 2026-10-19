// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func groupCompletionFixture(t *testing.T) (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, time.Time) {
	t.Helper()
	nc, reservations, _, now := groupGrantFixture(t)
	op := nc.Status.Configuration.Operation
	op.Phase = "Verified"
	op.TaskID = "group-task"
	submitted, verified := metav1.NewTime(now.Add(-9*time.Second)), metav1.NewTime(now.Add(-time.Second))
	op.SubmittedAt = &submitted
	nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{{Section: op.Section, Resource: "stable-fabric-resource", Operation: *op.DeepCopy(), VerifiedAt: &verified}}
	for _, r := range reservations {
		r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now.Add(-10 * time.Second))}
	}
	expected, err := GroupCompletionsForReceipt(nc, reservations, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range reservations {
		r.Status.Completion = expected[r.UID]
	}
	return nc, reservations, now
}

func TestGroupCompletionReceipts(t *testing.T) {
	for _, mode := range []string{"valid", "later-journal", "deleted", "released", "subset", "missing-receipt", "duplicate-receipt", "unverified", "unknown-journal", "missing-verified-time", "future-verified-time", "verified-before-submit", "claim-after-submit", "missing-claim", "changed-task", "changed-target", "changed-completion", "missing-member-completion", "replaced-controller", "replaced-reservation", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			nc, reservations, now := groupCompletionFixture(t)
			receipt := &nc.Status.Configuration.ActionApplications[0]
			switch mode {
			case "later-journal":
				nc.Status.Configuration.Operation = nil
				nc.Generation++
			case "deleted":
				at := metav1.NewTime(now)
				nc.DeletionTimestamp = &at
				reservations[0].DeletionTimestamp = &at
			case "released":
				for _, r := range reservations {
					r.Status.Grant.Phase = "Released"
				}
			case "subset":
				reservations = reservations[:1]
			case "missing-receipt":
				nc.Status.Configuration.ActionApplications = nil
			case "duplicate-receipt":
				nc.Status.Configuration.ActionApplications = append(nc.Status.Configuration.ActionApplications, *receipt.DeepCopy())
			case "unverified":
				receipt.Operation.Phase = "Accepted"
			case "unknown-journal":
				nc.Status.Configuration.Operation.Phase = "Unknown"
			case "missing-verified-time":
				receipt.VerifiedAt = nil
			case "future-verified-time":
				at := metav1.NewTime(now.Add(time.Second))
				receipt.VerifiedAt = &at
			case "verified-before-submit":
				at := metav1.NewTime(now.Add(-20 * time.Second))
				receipt.VerifiedAt = &at
			case "claim-after-submit":
				reservations[1].Status.Claim.ClaimedAt = metav1.NewTime(now)
			case "missing-claim":
				reservations[1].Status.Claim = nil
			case "changed-task":
				nc.Status.Configuration.Operation = nil
				receipt.Operation.TaskID = "other-task"
			case "changed-target":
				receipt.Operation.PhysicalGroup.Targets[0].Node.UID = "replacement"
			case "changed-completion":
				reservations[1].Status.Completion.OperationDigest = "changed"
			case "missing-member-completion":
				reservations[1].Status.Completion = nil
			case "replaced-controller":
				nc.UID = "replacement"
			case "replaced-reservation":
				reservations[1].UID = "replacement"
			case "cancelled":
				at := metav1.NewTime(now)
				reservations[1].Status.PreparationCancelledAt = &at
			}
			valid := mode == "valid" || mode == "later-journal" || mode == "deleted" || mode == "released"
			err := ValidateGroupCompletionReceipts(nc, reservations, now)
			if (err == nil) != valid {
				t.Fatal(mode, err)
			}
			if valid {
				repeated, err := GroupCompletionsForReceipt(nc, reservations, now.Add(time.Hour))
				if err != nil {
					t.Fatal(err)
				}
				for _, r := range reservations {
					if !repeated[r.UID].VerifiedAt.Equal(&r.Status.Completion.VerifiedAt) {
						t.Fatal("receipt time changed on recovery")
					}
					if _, err := CompletionForReceipt(r, nc, now); err == nil {
						t.Fatal("v1 completion accepted group")
					}
				}
			}
		})
	}
}
