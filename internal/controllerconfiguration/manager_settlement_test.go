// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func TestCancelledManagerSettlementEvidence(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	for _, scenario := range []string{"settled", "no manager", "before cancellation", "future manager", "future cancellation", "zero cancellation", "grant", "claim", "completion", "missing uid", "wrong protocol"} {
		t.Run(scenario, func(t *testing.T) {
			r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{UID: "reservation"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: Protocol}, Status: ops.ControllerConfigurationReservationStatus{PreparationCancelledAt: &metav1.Time{Time: now.Add(-time.Second)}, ManagerSettledAt: &metav1.Time{Time: now}}}
			switch scenario {
			case "no manager":
				r.Status.ManagerSettledAt = nil
			case "before cancellation":
				r.Status.ManagerSettledAt = &metav1.Time{Time: now.Add(-time.Minute)}
			case "future manager":
				r.Status.ManagerSettledAt = &metav1.Time{Time: now.Add(time.Minute)}
			case "future cancellation":
				r.Status.PreparationCancelledAt = &metav1.Time{Time: now.Add(time.Minute)}
			case "zero cancellation":
				r.Status.PreparationCancelledAt = &metav1.Time{}
			case "grant":
				r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Released", GrantedAt: metav1.NewTime(now.Add(-time.Minute))}
			case "claim":
				r.Status.Claim = &ops.ConfigurationReservationClaim{}
			case "completion":
				r.Status.Completion = &ops.ConfigurationReservationCompletion{}
			case "missing uid":
				r.UID = ""
			case "wrong protocol":
				r.Spec.Protocol = "other"
			}
			err := ValidateManagerSettlement(r, now)
			if (err == nil) != (scenario == "settled") {
				t.Fatalf("unexpected settlement result: %v", err)
			}
		})
	}
}
