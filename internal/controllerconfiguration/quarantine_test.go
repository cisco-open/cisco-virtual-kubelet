// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"testing"
	"time"
)

func TestQuarantineRequiresConclusiveRelease(t *testing.T) {
	_, r, _, now := settlementFixture(t)
	r.Status.Completion = &ops.ConfigurationReservationCompletion{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, TaskID: "task", SubmittedAt: metav1.NewTime(now.Add(-4 * time.Second)), VerifiedAt: metav1.NewTime(now)}
	r.Status.Grant.Phase = "Released"
	if RequiresQuarantine(r, now) {
		t.Fatal("valid released evidence blocked")
	}
	cases := map[string]func(*ops.ControllerConfigurationReservation){
		"held":                func(r *ops.ControllerConfigurationReservation) { r.Status.Grant.Phase = "Held" },
		"revoked claim":       func(r *ops.ControllerConfigurationReservation) { r.Status.Grant.Phase = "Revoked" },
		"unknown phase":       func(r *ops.ControllerConfigurationReservation) { r.Status.Grant.Phase = "Other" },
		"missing completion":  func(r *ops.ControllerConfigurationReservation) { r.Status.Completion = nil },
		"missing grant":       func(r *ops.ControllerConfigurationReservation) { r.Status.Grant = nil },
		"missing claim":       func(r *ops.ControllerConfigurationReservation) { r.Status.Claim = nil },
		"different operation": func(r *ops.ControllerConfigurationReservation) { r.Status.Completion.OperationID = "other" },
		"different digest":    func(r *ops.ControllerConfigurationReservation) { r.Status.Claim.OperationDigest = "other" },
		"future completion": func(r *ops.ControllerConfigurationReservation) {
			r.Status.Completion.VerifiedAt = metav1.NewTime(now.Add(time.Second))
		},
		"no task": func(r *ops.ControllerConfigurationReservation) { r.Status.Completion.TaskID = "" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rr := r.DeepCopy()
			mutate(rr)
			if !RequiresQuarantine(rr, now) {
				t.Fatal("uncertain work escaped quarantine")
			}
		})
	}
	r.Status.Claim = nil
	r.Status.Completion = nil
	if RequiresQuarantine(r, now) {
		t.Fatal("released preparation blocked")
	}
	r.Status.Grant = nil
	if RequiresQuarantine(r, now) {
		t.Fatal("empty reservation blocked")
	}
}
