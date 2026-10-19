// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestEnsureExistingReservationPreservesEvidence(t *testing.T) {
	for _, scenario := range []string{"verified journal", "collision", "missing finalizer", "stale controller"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			c, r, nc, now := settlementFixture(t)
			nc.Status.Configuration.Operation = nc.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
			if err := c.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			r.Finalizers = []string{Finalizer}
			if scenario == "collision" {
				r.Spec.InventoryID = "other"
			}
			if scenario == "missing finalizer" {
				r.Finalizers = nil
			}
			if err := c.Update(ctx, r); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			before := r.DeepCopy()
			if scenario == "stale controller" {
				nc.ResourceVersion = "0"
			}
			existing, err := EnsureReservation(ctx, c, nil, nc, nil, now)
			if scenario == "verified journal" {
				if err != nil || !reflect.DeepEqual(existing, before) {
					t.Fatal("existing claimed evidence not preserved", err)
				}
			} else if err == nil {
				t.Fatal("invalid existing reservation accepted")
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(r, before) {
				t.Fatal("ensure changed retained evidence")
			}
		})
	}
}
