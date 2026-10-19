// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func TestPhysicalTaskRecoveryRequiresClaimButNotFreshGrant(t *testing.T) {
	for _, scenario := range []string{"accepted", "old accepted task", "no claim", "wrong claim", "replacement worker", "released", "missing task", "before claim", "unknown"} {
		t.Run(scenario, func(t *testing.T) {
			ctx := context.Background()
			a, config := journalFixture(t)
			identity := configurationResourceIdentity{target: "device", serial: "SERIAL"}
			op, err := a.prepareConfiguration(ctx, config, nil, "sites", "device-action", "Update", identity)
			if err != nil {
				t.Fatal(err)
			}
			if err := a.startConfiguration(ctx, config, nil, op.ID, identity); err != nil {
				t.Fatal(err)
			}
			if err := a.advanceConfiguration(ctx, op.ID, "Accepted", "task"); err != nil {
				t.Fatal(err)
			}
			r, _ := settledConfigurationFixture(t, a)
			r.Status.Grant.Phase = "Held"
			r.Status.ManagerSettledAt = nil
			r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: r.Status.Grant.GrantedAt}
			a.workerPodName, a.workerPodUID = r.Spec.ControllerWorkerPod.Name, r.Spec.ControllerWorkerPod.UID
			nc, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "old accepted task":
				at := metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-time.Hour))
				nc.Status.Configuration.Operation.SubmittedAt = &at
				r.Status.Claim.ClaimedAt = metav1.NewTime(at.Add(-time.Second))
				r.Status.Grant.GrantedAt = r.Status.Claim.ClaimedAt
			case "no claim":
				r.Status.Claim = nil
			case "wrong claim":
				r.Status.Claim.OperationDigest = "wrong"
			case "replacement worker":
				a.workerPodUID = "replacement"
			case "released":
				r.Status.Grant.Phase = "Released"
			case "missing task":
				nc.Status.Configuration.Operation.TaskID = ""
			case "before claim":
				r.Status.Claim.ClaimedAt = metav1.NewTime(nc.Status.Configuration.Operation.SubmittedAt.Add(time.Second))
			case "unknown":
				nc.Status.Configuration.Operation.Phase = "Unknown"
			}
			if err := a.statusWriter.Status().Update(ctx, r); err != nil {
				t.Fatal(err)
			}
			if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			err = a.validatePhysicalConfigurationRecovery(ctx, nc)
			want := scenario == "accepted" || scenario == "old accepted task"
			if (err == nil) != want {
				t.Fatal("recovery decision", err, want)
			}
		})
	}
}
