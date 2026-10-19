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
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestPersistVerifiedGroupReceipt(t *testing.T) {
	for _, mode := range []string{"valid", "unknown", "changed-task", "subset", "missing-claim", "released", "cancelled", "missing-owner", "foreign-owner", "duplicate-owner", "duplicate-receipt", "foreign-resource", "bad-time", "denied", "nil-authorize", "late-controller", "late-member", "write-conflict", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupAuthorityFixture(t)
			op := nc.Status.Configuration.Operation
			op.Phase = "Verifying"
			op.TaskID = "task"
			submitted := metav1.NewTime(now.Add(-9 * time.Second))
			verifiedAt := metav1.NewTime(now.Add(-time.Second))
			op.SubmittedAt = &submitted
			nc.Status.Configuration.Owners = []cvk.NetworkControllerSectionOwner{{Section: op.Section, ConfigName: op.ConfigName, ConfigUID: op.ConfigUID}}
			receipt := &cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: "native-resource:policy-id", Operation: *op.DeepCopy(), VerifiedAt: &verifiedAt}
			receipt.Operation.Phase = "Verified"
			for _, r := range members {
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, ClaimedAt: metav1.NewTime(now.Add(-10 * time.Second))}
			}
			switch mode {
			case "unknown":
				op.Phase = "Unknown"
			case "changed-task":
				receipt.Operation.TaskID = "other"
			case "subset":
				members = members[:1]
			case "missing-claim":
				members[1].Status.Claim = nil
			case "released":
				members[1].Status.Grant.Phase = "Released"
			case "cancelled":
				at := metav1.NewTime(now)
				members[1].Status.PreparationCancelledAt = &at
			case "missing-owner":
				nc.Status.Configuration.Owners = nil
			case "foreign-owner":
				nc.Status.Configuration.Owners[0].ConfigUID = "other"
			case "duplicate-owner":
				nc.Status.Configuration.Owners = append(nc.Status.Configuration.Owners, nc.Status.Configuration.Owners[0])
			case "duplicate-receipt":
				nc.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{*receipt.DeepCopy()}
			case "foreign-resource":
				receipt.Section = "other"
			case "bad-time":
				receipt.VerifiedAt = &metav1.Time{Time: now.Add(time.Hour)}
			}
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			for _, r := range members {
				if err := c.Status().Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			original := nc.DeepCopy()
			authCalls := 0
			authorize := func(_ context.Context, rs []*ops.ControllerConfigurationReservation) error {
				authCalls++
				if mode == "denied" {
					return errors.New("denied")
				}
				if len(rs) != len(members) {
					t.Fatal("authorization received partial set")
				}
				if mode == "late-controller" {
					copy := nc.DeepCopy()
					copy.Labels = map[string]string{"changed": "true"}
					return c.Update(ctx, copy)
				}
				if mode == "late-member" {
					copy := members[0].DeepCopy()
					copy.Labels = map[string]string{"changed": "true"}
					return c.Update(ctx, copy)
				}
				// Mutating authorization's copies must not alter the submitted evidence.
				rs[0].Spec.OperationID = "mutated"
				return nil
			}
			if mode == "nil-authorize" {
				authorize = nil
			}
			writer := client.StatusClient(c)
			if mode == "lost-response" {
				writer = ambiguousStatus{Client: c}
			}
			writes := 0
			if mode == "write-conflict" {
				writer = groupAckRaceWriter{Client: c, before: func() error {
					writes++
					var changed cvk.NetworkController
					if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &changed); err != nil {
						return err
					}
					changed.Labels = map[string]string{"race": "true"}
					return c.Update(ctx, &changed)
				}}
			}

			err := PersistVerifiedGroupReceipt(ctx, c, writer, nc, members, receipt, authorize, now)
			if mode == "write-conflict" && writes != 1 {
				t.Fatal("status conflict retried", writes)
			}
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			var actual cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &actual); err != nil {
				t.Fatal(err)
			}
			persisted := mode == "valid" || mode == "lost-response"
			if (actual.Status.Configuration.Operation.Phase == "Verified") != persisted {
				t.Fatal("incorrect transition", actual.Status.Configuration.Operation.Phase)
			}
			if persisted {
				if len(actual.Status.Configuration.ActionApplications) != 1 || !equality.Semantic.DeepEqual(actual.Status.Configuration.ActionApplications[0], *receipt) || !equality.Semantic.DeepEqual(actual.Status.Configuration.Operation, &receipt.Operation) {
					t.Fatal("receipt/operation did not persist together")
				}
				if _, err := GroupCompletionsForReceipt(&actual, members, now); err != nil {
					t.Fatal("persisted receipt cannot drive existing completion flow", err)
				}
				if PersistVerifiedGroupReceipt(ctx, c, c, nc, members, receipt, authorize, now) == nil {
					t.Fatal("stale persistence replay accepted")
				}
			} else if !equality.Semantic.DeepEqual(actual.Status.Configuration, original.Status.Configuration) {
				t.Fatal("failed persistence modified configuration")
			}
			if !equality.Semantic.DeepEqual(nc, original) {
				t.Fatal("caller controller mutated")
			}
			if mode == "valid" && authCalls != 1 {
				t.Fatal(authCalls)
			}
		})
	}
}
