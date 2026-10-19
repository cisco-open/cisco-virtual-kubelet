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

func TestFinishCancelledGroup(t *testing.T) {
	for _, mode := range []string{"valid", "unsettled-peer", "unreleased-peer", "unknown", "missing-ack", "subset", "full-retention", "denied", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupCancelledManagerFixture(t)
			for _, r := range members {
				at := metav1.NewTime(now)
				r.Status.ManagerSettledAt = &at
				if err := c.Status().Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			switch mode {
			case "unsettled-peer":
				members[1].Status.ManagerSettledAt = nil
				if err := c.Status().Update(ctx, members[1]); err != nil {
					t.Fatal(err)
				}
			case "unreleased-peer":
				members[1].Status.Grant.Phase = "Held"
				if err := c.Status().Update(ctx, members[1]); err != nil {
					t.Fatal(err)
				}
			case "unknown":
				nc.Status.Configuration.Operation.Phase = "Unknown"
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "missing-ack":
				members[1].Status.GroupCancellation = nil
				if err := c.Status().Update(ctx, members[1]); err != nil {
					t.Fatal(err)
				}
			case "subset":
				members = members[:1]
			case "full-retention":
				nc.Status.Configuration.GroupCancellations = make([]cvk.NetworkControllerConfigurationGroupCancellation, 32)
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			retained := nc.Status.Configuration.DeepCopy()
			retained.Operation = nil
			operationID := nc.Status.Configuration.Operation.ID
			authorize := func(context.Context, []*ops.ControllerConfigurationReservation) error {
				if mode == "denied" {
					return errors.New("denied")
				}
				return nil
			}
			writer := client.StatusClient(c)
			if mode == "lost-response" {
				writer = ambiguousStatus{Client: c}
			}
			err := FinishCancelledGroup(ctx, c, writer, nc, members, authorize, now)
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			var actual cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &actual); err != nil {
				t.Fatal(err)
			}
			cleared := mode == "valid" || mode == "lost-response"
			if (actual.Status.Configuration.Operation == nil) != cleared {
				t.Fatal("incorrect journal retention")
			}
			if cleared {
				if len(actual.Status.Configuration.GroupCancellations) != 1 {
					t.Fatal("cancellation receipt missing")
				}
				if err := ValidateRetainedGroupCancellation(&actual, operationID, members, now); err != nil {
					t.Fatal(err)
				}
				if len(actual.Status.Configuration.ActionApplications) != 0 {
					t.Fatal("cancelled work recorded as success")
				}
				historical := actual.DeepCopy()
				historical.Status.Configuration.Operation = retainedOperationForCancellationTest(nc)
				deleting := copyGroupReservations(members)
				stamp := metav1.NewTime(now)
				deleting[0].DeletionTimestamp = &stamp
				if err := ValidateRetainedGroupCancellation(historical, operationID, deleting, now); err != nil {
					t.Fatal("historical receipt lost after journal advancement or deletion request", err)
				}
				withoutCancellation := actual.Status.Configuration.DeepCopy()
				withoutCancellation.GroupCancellations = nil
				if !equality.Semantic.DeepEqual(withoutCancellation, retained) {
					t.Fatal("unrelated evidence changed")
				}
				replacement := copyGroupReservations(members)
				replacement[0].UID = "replacement"
				if ValidateRetainedGroupCancellation(&actual, operationID, replacement, now) == nil {
					t.Fatal("replacement reservation inherited receipt")
				}
				actual.Status.Configuration.GroupCancellations[0].SettledAt = metav1.NewTime(now.Add(-time.Minute))
				if ValidateRetainedGroupCancellation(&actual, operationID, members, now) == nil {
					t.Fatal("receipt predating settlement accepted")
				}
			}
			if cleared && FinishCancelledGroup(ctx, c, c, nc, members, authorize, now) == nil {
				t.Fatal("stale clear accepted")
			}
		})
	}
}

func retainedOperationForCancellationTest(nc *cvk.NetworkController) *cvk.NetworkControllerConfigurationOperation {
	op := nc.Status.Configuration.Operation.DeepCopy()
	op.ID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	op.Phase = "Prepared"
	op.PhysicalGroup = nil
	op.CancellationRequestedAt = nil
	return op
}
