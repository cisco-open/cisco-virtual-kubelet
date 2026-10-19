// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	config "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type metadataClaimStore struct {
	client.Client
	mode    string
	updates int
	source  client.ObjectKey
}

func (s *metadataClaimStore) Status() client.SubResourceWriter {
	return metadataClaimStatus{s.Client.Status(), s}
}

type metadataClaimStatus struct {
	client.SubResourceWriter
	store *metadataClaimStore
}

func (w metadataClaimStatus) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	n := obj.(*cvk.NetworkController)
	w.store.updates++
	if w.store.mode == "conflict" {
		var other cvk.NetworkController
		if err := w.store.Client.Get(ctx, client.ObjectKeyFromObject(n), &other); err != nil {
			return err
		}
		other.Status.Phase = cvk.NetworkControllerPhaseConnecting
		if err := w.SubResourceWriter.Update(ctx, &other); err != nil {
			return err
		}
	}
	if w.store.mode == "pruned" {
		n = n.DeepCopy()
		n.Status.Configuration.Operation.SubmittedAt = nil
	}
	if err := w.SubResourceWriter.Update(ctx, n, opts...); err != nil {
		return err
	}
	if w.store.mode == "lost-ack" {
		return errors.New("claim status response lost")
	}
	if w.store.mode == "post-source-drift" {
		var source config.NetworkControllerConfig
		if err := w.store.Client.Get(ctx, w.store.source, &source); err != nil {
			return err
		}
		source.Generation++
		return w.store.Client.Update(ctx, &source)
	}
	return nil
}

func TestMetadataClaimOnce(t *testing.T) {
	for _, mode := range []string{"valid", "concurrent-check", "mutate-arguments", "nil-authorizer", "denied", "wrong-pod", "not-ready", "future-ready", "partial-fences", "replacement", "source-drift", "lost-ack", "conflict", "pruned", "post-source-drift", "check-denied", "check-source-drift", "zero-check-time", "condition-drift", "cancelled-check", "controller-generation"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, nc, source, policy := metadataAcquisitionFixture(t)
			acquisition := &metadataAcquisitionStore{Client: c, mode: "valid", source: client.ObjectKeyFromObject(source)}
			ready, err := AcquireMetadataFenceCohort(ctx, c, acquisition, nc, bootstrapFenceTestInstallation(), policy, []string{"bootstrap123", "second123"}, func(context.Context, *cvk.NetworkController, MetadataScopeWorkerObservation, AdmissionProtection) error {
				return nil
			}, time.Now())
			if err != nil {
				t.Fatal(err)
			}
			nc = ready
			before := nc.DeepCopy()
			switch mode {
			case "not-ready":
				nc.Status.Configuration.MetadataHandoff.ReadyAt = nil
			case "future-ready":
				at := nc.Status.Configuration.MetadataHandoff.ReadyAt.DeepCopy()
				at.Time = at.Add(time.Hour)
				nc.Status.Configuration.MetadataHandoff.ReadyAt = at
			case "partial-fences":
				nc.Status.Configuration.MetadataHandoff.Fences = nc.Status.Configuration.MetadataHandoff.Fences[:1]
			}
			if mode == "not-ready" || mode == "future-ready" || mode == "partial-fences" {
				if err := c.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "source-drift" {
				source.Generation++
				if err := c.Update(ctx, source); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "controller-generation" {
				nc.Spec.CredentialSecretRef.Name = "rotated-controller-credentials"
				nc.Generation++
				if err := c.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
					t.Fatal(err)
				}
				before = nc.DeepCopy()
			}
			store := &metadataClaimStore{Client: c, mode: mode, source: client.ObjectKeyFromObject(source)}
			var reader client.Reader = c
			if mode == "replacement" {
				reader = &metadataAcquisitionStore{Client: c, mode: "published-replacement"}
			}
			pod := nc.Status.Configuration.MetadataHandoff.WorkerPod
			if mode == "wrong-pod" {
				pod.UID = "replacement"
			}
			var calls atomic.Int32
			denyCheck := atomic.Bool{}
			denyCheck.Store(mode == "check-denied")
			authorize := MetadataWorkerAuthorization(func(_ context.Context, n *cvk.NetworkController) error {
				count := calls.Add(1)
				if mode == "denied" || denyCheck.Load() && count > 2 {
					return errors.New("worker authority denied")
				}
				if mode == "mutate-arguments" {
					n.Status.Configuration.Operation.ID = "foreign"
					n.Status.Configuration.MetadataHandoff.Fences[0].UID = "foreign"
				}
				return nil
			})
			if mode == "nil-authorizer" {
				authorize = nil
			}
			permit, err := ClaimMetadataOnce(ctx, reader, store, nc, pod, bootstrapFenceTestInstallation(), policy, authorize, time.Now())
			good := mode == "valid" || mode == "concurrent-check" || mode == "mutate-arguments" || mode == "check-denied" || mode == "check-source-drift" || mode == "zero-check-time" || mode == "condition-drift" || mode == "cancelled-check"
			if (err == nil) != good || good && permit == nil || !good && permit != nil {
				t.Fatal("incorrect one-use claim result", err)
			}
			var current cvk.NetworkController
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), &current); err != nil {
				t.Fatal(err)
			}
			if mode != "not-ready" && mode != "future-ready" && mode != "partial-fences" && !reflect.DeepEqual(before, nc) {
				t.Fatal("claim mutated caller snapshot")
			}
			if !reflect.DeepEqual(before.Status.Configuration.ActionApplications, current.Status.Configuration.ActionApplications) {
				t.Fatal("claim fabricated a native effect")
			}
			if !good {
				if mode == "lost-ack" || mode == "pruned" || mode == "post-source-drift" {
					if current.Status.Configuration.Operation.Phase != "Submitting" {
						t.Fatal("uncertain claim lost durable fence")
					}
					if _, err := ClaimMetadataOnce(ctx, c, store, &current, pod, bootstrapFenceTestInstallation(), policy, authorize, time.Now()); err == nil || store.updates != 1 {
						t.Fatal("uncertain persisted claim replayed", err)
					}
				} else if mode == "conflict" {
					if current.Status.Configuration.Operation.Phase != "Prepared" {
						t.Fatal("failed CAS changed operation")
					}
					store.mode = "valid"
					if _, err := ClaimMetadataOnce(ctx, c, store, &current, pod, bootstrapFenceTestInstallation(), policy, authorize, time.Now()); err != nil {
						t.Fatal("fresh unclaimed conflict recovery failed", err)
					}
				} else if store.updates != 0 {
					t.Fatal("denied claim wrote status")
				}
				return
			}
			if store.updates != 1 || current.Status.Configuration.Operation.Phase != "Submitting" || current.Status.Configuration.Operation.SubmittedAt == nil {
				t.Fatal("original journal was not claimed exactly once")
			}
			if _, err := ClaimMetadataOnce(ctx, c, store, &current, pod, bootstrapFenceTestInstallation(), policy, authorize, time.Now()); err == nil || store.updates != 1 {
				t.Fatal("persisted Submitting reconstructed a permit", err)
			}
			copyPermit := *permit
			if mode == "concurrent-check" {
				outcomes := make(chan error, 2)
				go func() { outcomes <- permit.Check(ctx, time.Now()) }()
				go func() { outcomes <- copyPermit.Check(ctx, time.Now()) }()
				a, b := <-outcomes, <-outcomes
				if (a == nil) == (b == nil) {
					t.Fatal("permit copies did not share one opportunity", a, b)
				}
				return
			}
			if mode == "condition-drift" {
				current.Status.Phase = cvk.NetworkControllerPhaseConnecting
				if err := c.Status().Update(ctx, &current); err != nil {
					t.Fatal(err)
				}
			}
			checkTime := time.Now()
			if mode == "check-source-drift" {
				source.Generation++
				if err := c.Update(ctx, source); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "zero-check-time" {
				checkTime = time.Time{}
			}
			checkCtx := ctx
			if mode == "cancelled-check" {
				var cancel context.CancelFunc
				checkCtx, cancel = context.WithCancel(ctx)
				cancel()
			}
			err = permit.Check(checkCtx, checkTime)
			denied := mode == "check-denied" || mode == "check-source-drift" || mode == "zero-check-time" || mode == "cancelled-check"
			if (err != nil) != denied {
				t.Fatal("incorrect dispatch check", err)
			}
			denyCheck.Store(false)
			if mode == "check-source-drift" {
				source.Generation--
				if err := c.Update(ctx, source); err != nil {
					t.Fatal(err)
				}
			}
			if err := permit.Reobserve(ctx, time.Now()); (err != nil) != denied {
				t.Fatal("winning/denied epoch reobservation mismatch", err)
			}
			if err := copyPermit.Check(ctx, time.Now()); err == nil {
				t.Fatal("spent/refused permit copy replayed")
			}
		})
	}
}
