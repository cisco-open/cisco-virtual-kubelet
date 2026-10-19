// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"testing"
)

func TestCancelPreparation(t *testing.T) {
	for _, scenario := range []string{"ungranted", "granted", "claimed", "wrong pod", "stale version", "repeat"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			c, r, _, now := claimFixture(t)
			r.Spec.DeviceWorkerPod = ops.ConfigurationReservationReference{Name: "worker", UID: "pod-uid"}
			if err := c.Update(ctx, r); err != nil {
				t.Fatal(err)
			}
			r.Status = ops.ControllerConfigurationReservationStatus{}
			if scenario == "granted" {
				r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held"}
			}
			if scenario == "claimed" {
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: r.Spec.OperationID}
			}
			if scenario == "repeat" {
				r.Status.PreparationCancelledAt = &metav1.Time{Time: now}
			}
			if err := c.Status().Update(ctx, r); err != nil {
				t.Fatal(err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			podUID := "pod-uid"
			if scenario == "wrong pod" {
				podUID = "other"
			}
			if scenario == "stale version" {
				r.ResourceVersion = "old"
			}
			err := CancelPreparation(ctx, c, c, r, podUID, now)
			success := scenario == "ungranted" || scenario == "repeat"
			if (err == nil) != success {
				t.Fatalf("err=%v", err)
			}
			if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
				t.Fatal(err)
			}
			if success {
				if r.Status.PreparationCancelledAt == nil || r.Status.Grant != nil || r.Status.Claim != nil || r.Status.ManagerSettledAt != nil {
					t.Fatal("cancellation manufactured grant, claim or settlement")
				}
				if err := CancelPreparation(ctx, c, c, r, podUID, now); err != nil {
					t.Fatal("idempotent cancellation", err)
				}
			} else if r.Status.PreparationCancelledAt != nil {
				t.Fatal("rejected cancellation changed status")
			}
		})
	}
}
