// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRetireCancelledGroupReservation(t *testing.T) {
	for _, mode := range []string{"valid", "sequential", "canonical-targets", "missing-receipt", "duplicate-receipt", "partial-members", "duplicate-member", "foreign-member", "wrong-uid", "future-receipt", "early-receipt", "unknown", "active-journal", "missing-ack", "unsettled", "held", "fake-completion", "denied", "lost-finalizer-response"} {
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
			if err := FinishCancelledGroup(ctx, c, c, nc, members, func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, now); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			receipt := &nc.Status.Configuration.GroupCancellations[0]
			r := members[0]
			switch mode {
			case "canonical-targets":
				receipt.Operation.PhysicalGroup.Targets[0].PhysicalIdentity = strings.ToUpper(receipt.Operation.PhysicalGroup.Targets[0].PhysicalIdentity)
			case "missing-receipt":
				nc.Status.Configuration.GroupCancellations = nil
			case "duplicate-receipt":
				nc.Status.Configuration.GroupCancellations = append(nc.Status.Configuration.GroupCancellations, *receipt.DeepCopy())
			case "partial-members":
				receipt.Members = receipt.Members[:1]
			case "duplicate-member":
				receipt.Members[1] = receipt.Members[0]
			case "foreign-member":
				receipt.Members[1].Name = "foreign"
			case "wrong-uid":
				for i := range receipt.Members {
					if receipt.Members[i].Name == r.Name {
						receipt.Members[i].UID = "replacement"
					}
				}
			case "future-receipt":
				receipt.SettledAt = metav1.NewTime(now.Add(time.Hour))
			case "early-receipt":
				receipt.SettledAt = metav1.NewTime(now.Add(-time.Hour))
			case "unknown":
				receipt.Operation.Phase = "Unknown"
			case "active-journal":
				nc.Status.Configuration.Operation = receipt.Operation.DeepCopy()
			case "missing-ack":
				r.Status.GroupCancellation = nil
			case "unsettled":
				r.Status.ManagerSettledAt = nil
			case "held":
				r.Status.Grant.Phase = "Held"
			case "fake-completion":
				r.Status.Completion = &ops.ConfigurationReservationCompletion{TaskID: "fake"}
			}
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if err := c.Status().Update(ctx, r); err != nil {
				t.Fatal(err)
			}
			before := nc.Status.Configuration.DeepCopy()
			allow := func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
			if mode == "sequential" {
				if err := RetireCancelledGroupReservation(ctx, c, c, members[1], allow, now); err != nil {
					t.Fatal(err)
				}
			}
			authorize := allow
			if mode == "denied" {
				authorize = func(context.Context, *ops.ControllerConfigurationReservation) error { return errors.New("denied") }
			}
			writer := client.Writer(c)
			if mode == "lost-finalizer-response" {
				writer = lostGroupRetirementWriter{Client: c}
			}
			err := RetireCancelledGroupReservation(ctx, c, writer, r, authorize, now)
			success := mode == "valid" || mode == "sequential" || mode == "canonical-targets"
			if (err == nil) != success {
				t.Fatal(mode, err)
			}
			var actual ops.ControllerConfigurationReservation
			readErr := c.Get(ctx, client.ObjectKeyFromObject(r), &actual)
			if success {
				if !apierrors.IsNotFound(readErr) {
					t.Fatal("cancelled row retained", readErr)
				}
				if err := RetireCancelledGroupReservation(ctx, c, c, r, allow, now); err != nil {
					t.Fatal("absent row not idempotent", err)
				}
			} else {
				if readErr != nil {
					t.Fatal("failed retirement lost member", readErr)
				}
				if mode == "lost-finalizer-response" {
					if len(actual.Finalizers) != 0 {
						t.Fatal("finalizer removal did not persist")
					}
					if err := RetireCancelledGroupReservation(ctx, c, c, &actual, allow, now); err != nil {
						t.Fatal("fresh resume failed", err)
					}
				}
			}
			var retained cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &retained); err != nil {
				t.Fatal(err)
			}
			if !equality.Semantic.DeepEqual(retained.Status.Configuration, before) {
				t.Fatal("cancellation receipt/fence changed")
			}
			if len(retained.Status.Configuration.ActionApplications) != 0 {
				t.Fatal("cancellation fabricated success")
			}
			if mode != "sequential" {
				var peer ops.ControllerConfigurationReservation
				if err := c.Get(ctx, client.ObjectKeyFromObject(members[1]), &peer); err != nil {
					t.Fatal(err)
				}
				if !equality.Semantic.DeepEqual(peer.Status, members[1].Status) {
					t.Fatal("peer changed")
				}
			}
		})
	}
}
