// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"strings"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRetireSettledGroupReservation(t *testing.T) {
	for _, mode := range []string{"valid", "sequential", "canonical-targets", "active-journal", "missing-receipt", "unknown-receipt", "unsettled", "held", "changed-completion", "replaced-member", "missing-controller", "denied", "nil-authorize", "late-controller", "late-member", "lost-finalizer-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, members, now := groupManagerFixture(t)
			for _, r := range members {
				at := metav1.NewTime(now)
				r.Status.ManagerSettledAt = &at
				if err := c.Status().Update(ctx, r); err != nil {
					t.Fatal(err)
				}
			}
			if err := FinishVerifiedGroup(ctx, c, c, nc, members, func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, now); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			r := members[0]
			switch mode {
			case "canonical-targets":
				nc.Status.Configuration.ActionApplications[0].Operation.PhysicalGroup.Targets[0].PhysicalIdentity = strings.ToUpper(nc.Status.Configuration.ActionApplications[0].Operation.PhysicalGroup.Targets[0].PhysicalIdentity)
			case "active-journal":
				nc.Status.Configuration.Operation = nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
			case "missing-receipt":
				nc.Status.Configuration.ActionApplications = nil
			case "unknown-receipt":
				nc.Status.Configuration.ActionApplications[0].Operation.Phase = "Unknown"
			case "unsettled":
				r.Status.ManagerSettledAt = nil
			case "held":
				r.Status.Grant.Phase = "Held"
			case "changed-completion":
				r.Status.Completion.TaskID = "other"
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
				if err := RetireSettledGroupReservation(ctx, c, c, members[1], allow, now); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing-controller" {
				if err := c.Delete(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			expected := r.DeepCopy()
			if mode == "replaced-member" {
				expected.UID = "other-uid"
			}
			authorize := func(context.Context, *ops.ControllerConfigurationReservation) error {
				switch mode {
				case "denied":
					return errors.New("denied")
				case "late-controller":
					copy := nc.DeepCopy()
					copy.Labels = map[string]string{"changed": "true"}
					return c.Update(ctx, copy)
				case "late-member":
					copy := r.DeepCopy()
					copy.Labels = map[string]string{"changed": "true"}
					return c.Update(ctx, copy)
				}
				return nil
			}
			if mode == "nil-authorize" {
				authorize = nil
			}
			writer := client.Writer(c)
			if mode == "lost-finalizer-response" {
				writer = lostGroupRetirementWriter{Client: c}
			}
			err := RetireSettledGroupReservation(ctx, c, writer, expected, authorize, now)
			success := mode == "valid" || mode == "sequential" || mode == "canonical-targets"
			if (err == nil) != success {
				t.Fatal(mode, err)
			}
			var actual ops.ControllerConfigurationReservation
			readErr := c.Get(ctx, client.ObjectKeyFromObject(r), &actual)
			if success {
				if !apierrors.IsNotFound(readErr) {
					t.Fatal("settled reservation retained", readErr)
				}
				if err := RetireSettledGroupReservation(ctx, c, c, expected, allow, now); err != nil {
					t.Fatal("absent reservation not idempotent", err)
				}
			} else {
				if readErr != nil {
					t.Fatal("failed retirement lost row", readErr)
				}
				if mode == "lost-finalizer-response" {
					if len(actual.Finalizers) != 0 {
						t.Fatal("finalizer write did not persist")
					}
					if err := RetireSettledGroupReservation(ctx, c, c, &actual, allow, now); err != nil {
						t.Fatal("fresh resume failed", err)
					}
				}
			}
			if mode != "missing-controller" {
				var retained cvk.NetworkController
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &retained); err != nil {
					t.Fatal(err)
				}
				if !equality.Semantic.DeepEqual(retained.Status.Configuration, before) {
					t.Fatal("retirement removed ownership or action receipt")
				}
			}
			if mode != "sequential" {
				var peer ops.ControllerConfigurationReservation
				if err := c.Get(ctx, client.ObjectKeyFromObject(members[1]), &peer); err != nil {
					t.Fatal("peer modified", err)
				}
				if !equality.Semantic.DeepEqual(peer.Status, members[1].Status) {
					t.Fatal("peer status modified")
				}
			}
		})
	}
}

type lostGroupRetirementWriter struct{ client.Client }

func (w lostGroupRetirementWriter) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if err := w.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return errors.New("lost finalizer write response")
}
