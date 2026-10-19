// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestGroupSubmissionAndDispatchPermits(t *testing.T) {
	for _, mode := range []string{"valid", "concurrent-dispatch", "pruned-journal", "deny-start", "deny-dispatch", "lost-journal-response", "revoked-before-dispatch", "changed-controller", "changed-claim"} {
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
			calls := 0
			authorize := func(ctx context.Context, _ []*ops.ControllerConfigurationReservation) error {
				calls++
				if (mode == "deny-start" && calls == 2) || (mode == "deny-dispatch" && calls == 3) {
					return errors.New("authorization changed")
				}
				if mode == "changed-controller" && calls == 2 {
					current := nc.DeepCopy()
					current.Spec.Paused = true
					return c.Update(ctx, current)
				}
				return nil
			}
			writer := client.StatusClient(c)
			if mode == "lost-journal-response" || mode == "pruned-journal" {
				writer = lostGroupJournalWriter{Client: c, prune: mode == "pruned-journal"}
			}
			permit, err := ClaimGroupForController(ctx, c, writer, nc, reservations, "leases", authorize, now)
			if err != nil || permit == nil {
				t.Fatal(err)
			}
			alias := *permit
			dispatch, err := permit.BeginSubmission(ctx, now)
			failedStart := mode == "deny-start" || mode == "lost-journal-response" || mode == "pruned-journal" || mode == "changed-controller"
			if (err != nil) != failedStart {
				t.Fatal("unexpected transition", err)
			}
			if failedStart && dispatch != nil {
				t.Fatal("failed transition returned dispatch authority")
			}
			if retry, err := alias.BeginSubmission(ctx, now); err == nil || retry != nil {
				t.Fatal("copied submission permit replayed")
			}
			if !failedStart {
				if mode == "revoked-before-dispatch" || mode == "changed-claim" {
					var r ops.ControllerConfigurationReservation
					if err := c.Get(ctx, client.ObjectKeyFromObject(reservations[1]), &r); err != nil {
						t.Fatal(err)
					}
					if mode == "changed-claim" {
						r.Status.Claim.OperationDigest = "changed"
					} else {
						r.Status.Grant.Phase = "Revoked"
					}
					if err := c.Status().Update(ctx, &r); err != nil {
						t.Fatal(err)
					}
				}
				copied := *dispatch
				if mode == "concurrent-dispatch" {
					var wg sync.WaitGroup
					var succeeded atomic.Int32
					for range 8 {
						wg.Add(1)
						go func() {
							defer wg.Done()
							if copied.Check(ctx, now) == nil {
								succeeded.Add(1)
							}
						}()
					}
					wg.Wait()
					if succeeded.Load() != 1 {
						t.Fatal("concurrent permit reuse", succeeded.Load())
					}
					err = nil
				} else {
					err = dispatch.Check(ctx, now)
				}
				if (err == nil) != (mode == "valid" || mode == "concurrent-dispatch") {
					t.Fatal("unexpected dispatch result", mode, err)
				}
				if copied.Check(ctx, now) == nil {
					t.Fatal("copied dispatch permit replayed")
				}
			}
			var current cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &current); err != nil {
				t.Fatal(err)
			}
			expectedPhase := "Submitting"
			if mode == "deny-start" || mode == "changed-controller" {
				expectedPhase = "Prepared"
			}
			if current.Status.Configuration.Operation.Phase != expectedPhase {
				t.Fatal("journal fence lost", current.Status.Configuration.Operation.Phase)
			}
			for _, r := range reservations {
				if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
					t.Fatal(err)
				}
				if r.Status.Claim == nil {
					t.Fatal("failed permit cleared claims")
				}
			}
		})
	}
}

type lostGroupJournalWriter struct {
	client.Client
	prune bool
}

func (w lostGroupJournalWriter) Status() client.SubResourceWriter {
	return lostGroupJournalStatus{SubResourceWriter: w.Client.Status(), prune: w.prune}
}

type lostGroupJournalStatus struct {
	client.SubResourceWriter
	prune bool
}

func (w lostGroupJournalStatus) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	if nc, ok := obj.(*cvk.NetworkController); ok && w.prune {
		nc.Status.Configuration.Operation.PhysicalGroup = nil
	}
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if _, ok := obj.(*cvk.NetworkController); ok && !w.prune {
		return errors.New("journal response lost")
	}
	return nil
}
