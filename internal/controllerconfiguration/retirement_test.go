// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestRetirementRequiresClearedJournalAndSettlement(t *testing.T) {
	for _, scenario := range []string{"complete", "new journal", "no marker", "held", "journal retained", "receipt missing", "replaced controller", "stale reservation", "replacement reservation", "ambiguous removal"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			c, r, nc, now := settlementFixture(t)
			if err := PublishCompletion(ctx, c, c, r, now); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			r.Status.Grant.Phase = "Released"
			r.Status.ManagerSettledAt = &metav1.Time{Time: now}
			if scenario == "no marker" {
				r.Status.ManagerSettledAt = nil
			}
			if scenario == "held" {
				r.Status.Grant.Phase = "Held"
			}
			if err := c.Status().Update(ctx, r); err != nil {
				t.Fatal(err)
			}
			r.Finalizers = []string{Finalizer}
			if err := c.Update(ctx, r); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
				t.Fatal(err)
			}
			if scenario == "journal retained" {
				nc.Status.Configuration.Operation = nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
			}
			if scenario == "new journal" {
				nc.Status.Configuration.Operation = nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
				nc.Status.Configuration.Operation.ID = "new-operation"
				nc.Status.Configuration.Operation.Phase = "Verifying"
			}
			if scenario == "receipt missing" {
				nc.Status.Configuration.ActionApplications = nil
			}
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			if scenario == "replaced controller" {
				if err := c.Delete(ctx, nc); err != nil {
					t.Fatal(err)
				}
				nc.UID = "replacement"
				nc.ResourceVersion = ""
				if err := c.Create(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			before := r.DeepCopy()
			expected := r.DeepCopy()
			if scenario == "stale reservation" {
				expected.ResourceVersion = "0"
			}
			if scenario == "replacement reservation" {
				expected.UID = "other"
			}
			var writer client.Writer = c
			if scenario == "ambiguous removal" {
				writer = ambiguousRetirementWriter{Client: c}
			}
			err := RetireSettledReservation(ctx, c, writer, expected, now)
			if scenario == "complete" || scenario == "new journal" {
				if err != nil {
					t.Fatal(err)
				}
				if err := c.Get(ctx, client.ObjectKeyFromObject(r), &ops.ControllerConfigurationReservation{}); !apierrors.IsNotFound(err) {
					t.Fatal("reservation not retired", err)
				}
				if err := RetireSettledReservation(ctx, c, c, expected, now); err != nil {
					t.Fatal("retry", err)
				}
				return
			}
			if err == nil {
				t.Fatal("unsafe retirement accepted")
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal("evidence deleted", err)
			}
			if scenario == "ambiguous removal" {
				if !reflect.DeepEqual(r.Status, before.Status) {
					t.Fatal("evidence changed")
				}
				if err := RetireSettledReservation(ctx, c, c, r, now); err != nil {
					t.Fatal("fresh retry did not retire", err)
				}
			} else if !reflect.DeepEqual(r, before) {
				t.Fatal("rejected retirement changed evidence")
			}
		})
	}
}

type ambiguousRetirementWriter struct{ client.Client }

func (w ambiguousRetirementWriter) Update(ctx context.Context, obj client.Object, opts ...client.UpdateOption) error {
	if err := w.Client.Update(ctx, obj, opts...); err != nil {
		return err
	}
	return errors.New("response lost after finalizer update")
}
