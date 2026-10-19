// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestClaimGroupOnce(t *testing.T) {
	for _, mode := range []string{"valid", "authorize-denied", "no-authorizer", "callback-copy", "lost-first-response", "conflict-second", "revoked-second", "controller-changed", "authorization-revokes"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			nc, reservations, leases, now := groupGrantFixture(t)
			scheme := runtime.NewScheme()
			_ = cvk.AddToScheme(scheme)
			_ = ops.AddToScheme(scheme)
			_ = coordination.AddToScheme(scheme)
			objects := []client.Object{nc}
			for _, r := range reservations {
				objects = append(objects, r)
			}
			for _, l := range leases {
				objects = append(objects, l)
			}
			c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(nc, &ops.ControllerConfigurationReservation{}).WithObjects(objects...).Build()
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			for _, r := range reservations {
				if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
					t.Fatal(err)
				}
			}
			writer := &groupClaimTestWriter{Client: c, mode: mode, controller: nc}
			authorize := func(ctx context.Context, copy []*ops.ControllerConfigurationReservation) error {
				switch mode {
				case "authorize-denied":
					return errors.New("denied")
				case "callback-copy":
					copy[0].Spec.InventoryID = "changed-by-callback"
				case "authorization-revokes":
					fresh := reservations[1].DeepCopy()
					fresh.Status.Grant.Phase = "Revoked"
					return c.Status().Update(ctx, fresh)
				}
				return nil
			}
			if mode == "no-authorizer" {
				authorize = nil
			}
			result, err := ClaimGroupOnce(ctx, c, writer, nc, reservations, "leases", authorize, now)
			valid := mode == "valid" || mode == "callback-copy"
			if (err == nil) != valid {
				t.Fatal(mode, err)
			}
			if !valid && result != nil {
				t.Fatal("returned partial claims")
			}
			claimed := 0
			for _, r := range reservations {
				if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
					t.Fatal(err)
				}
				if r.Status.Claim != nil {
					claimed++
				}
			}
			want := 0
			switch mode {
			case "valid", "callback-copy":
				want = 2
			case "lost-first-response", "conflict-second", "revoked-second", "controller-changed":
				want = 1
			}
			if claimed != want {
				t.Fatal("incorrect retained claim count", claimed, want)
			}
			if valid {
				if len(result) != 2 {
					t.Fatal("incomplete result")
				}
				before := writer.calls
				retry, err := ClaimGroupOnce(ctx, c, writer, nc, reservations, "leases", authorize, now)
				if err == nil || retry != nil || writer.calls != before {
					t.Fatal("existing claims replayed")
				}
			}
		})
	}
}

type groupClaimTestWriter struct {
	client.Client
	mode       string
	calls      int
	controller *cvk.NetworkController
}

func (w *groupClaimTestWriter) Status() client.SubResourceWriter {
	return &groupClaimTestStatus{SubResourceWriter: w.Client.Status(), parent: w}
}

type groupClaimTestStatus struct {
	client.SubResourceWriter
	parent *groupClaimTestWriter
}

func (w *groupClaimTestStatus) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	p := w.parent
	p.calls++
	if p.calls == 2 && p.mode == "conflict-second" {
		return errors.New("simulated conflict")
	}
	if p.calls == 2 && p.mode == "revoked-second" {
		var revoked ops.ControllerConfigurationReservation
		if err := p.Client.Get(ctx, client.ObjectKeyFromObject(obj), &revoked); err != nil {
			return err
		}
		revoked.Status.Grant.Phase = "Revoked"
		if err := p.Client.Status().Update(ctx, &revoked); err != nil {
			return err
		}
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if p.calls == 1 && p.mode == "lost-first-response" {
		return errors.New("simulated lost response")
	}
	if p.calls == 1 && p.mode == "controller-changed" {
		var nc cvk.NetworkController
		if err := p.Client.Get(ctx, client.ObjectKeyFromObject(p.controller), &nc); err != nil {
			return err
		}
		nc.Spec.Paused = true
		return p.Client.Update(ctx, &nc)
	}
	return nil
}
