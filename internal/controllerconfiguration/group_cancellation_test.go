// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"sync"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestGroupCancellationBoundary(t *testing.T) {
	for _, mode := range []string{"valid", "paused", "Submitting", "Accepted", "Unknown", "Verified", "denied", "lost-response"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, now := groupReaderFixture(t)
			switch mode {
			case "paused":
				nc.Spec.Paused = true
				if err := c.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			case "Submitting", "Accepted", "Unknown", "Verified":
				nc.Status.Configuration.Operation.Phase = mode
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			writer := client.StatusClient(c)
			if mode == "lost-response" {
				writer = ambiguousStatus{Client: c}
			}
			authorize := func(context.Context, *cvk.NetworkController) error {
				if mode == "denied" {
					return errors.New("denied")
				}
				return nil
			}
			originalDigest := nc.Status.Configuration.Operation.PhysicalGroup.Digest
			err := BeginGroupCancellation(ctx, c, writer, nc, authorize, now)
			if (err == nil) != (mode == "valid" || mode == "paused") {
				t.Fatal(mode, err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			fenced := mode == "valid" || mode == "paused" || mode == "lost-response"
			if (ValidateGroupCancellationFence(nc, now) == nil) != fenced {
				t.Fatal("incorrect cancellation fence")
			}
			if fenced {
				before := nc.ResourceVersion
				if err := BeginGroupCancellation(ctx, c, c, nc, authorize, now); err != nil {
					t.Fatal("durable cancellation not recoverable", err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
					t.Fatal(err)
				}
				if nc.ResourceVersion != before || nc.Status.Configuration.Operation.PhysicalGroup.Digest != originalDigest {
					t.Fatal("cancellation replay changed identity")
				}
			}
		})
	}
}

func TestGroupCancellationRacesSubmission(t *testing.T) {
	ctx := context.Background()
	c, nc, members, now := groupAuthorityFixture(t)
	permit, err := ClaimGroupForController(ctx, c, c, nc, members, "leases", func(context.Context, []*ops.ControllerConfigurationReservation) error { return nil }, now)
	if err != nil {
		t.Fatal(err)
	}
	var cancelErr, submitErr error
	var dispatch *GroupDispatchPermit
	var wg sync.WaitGroup
	wg.Add(2)
	start := make(chan struct{})
	go func() {
		defer wg.Done()
		<-start
		cancelErr = BeginGroupCancellation(ctx, c, c, nc, func(context.Context, *cvk.NetworkController) error { return nil }, now)
	}()
	go func() { defer wg.Done(); <-start; dispatch, submitErr = permit.BeginSubmission(ctx, now) }()
	close(start)
	wg.Wait()
	if (cancelErr == nil) == (submitErr == nil) {
		t.Fatal("cancellation and submission must have exactly one winner", cancelErr, submitErr)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	if cancelErr == nil {
		if dispatch != nil || ValidateGroupCancellationFence(nc, now) != nil {
			t.Fatal("cancelled operation gained submission permit")
		}
	} else if nc.Status.Configuration.Operation.Phase != "Submitting" || dispatch == nil {
		t.Fatal("submission winner not retained")
	}
	for _, r := range members {
		if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
			t.Fatal(err)
		}
		if r.Status.Claim == nil {
			t.Fatal("race cleared durable claim")
		}
	}
}
