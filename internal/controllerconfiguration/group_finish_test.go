// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestFinishVerifiedGroup(t *testing.T) {
	for _, mode := range []string{"valid", "unsettled-peer", "unreleased-peer", "unknown", "missing-receipt", "denied", "lost-response"} {
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
			case "missing-receipt":
				nc.Status.Configuration.ActionApplications = nil
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			retained := nc.Status.Configuration.DeepCopy()
			retained.Operation = nil
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
			err := FinishVerifiedGroup(ctx, c, writer, nc, members, authorize, now)
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
			if cleared && !equality.Semantic.DeepEqual(actual.Status.Configuration, retained) {
				t.Fatal("receipt or ownership lost")
			}
			if cleared && FinishVerifiedGroup(ctx, c, c, nc, members, authorize, now) == nil {
				t.Fatal("stale clear accepted")
			}
		})
	}
}
