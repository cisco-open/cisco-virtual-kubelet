// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"testing"
	"time"
)

func TestCompletionRequiresPersistedReceipt(t *testing.T) {
	_, r, op, now := claimFixture(t)
	r.Spec.Controller.Name = "cc"
	r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now.Add(-5 * time.Second))}
	op.Phase = "Verified"
	op.TaskID = "task"
	submitted := metav1.NewTime(now.Add(-4 * time.Second))
	op.SubmittedAt = &submitted
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: r.Namespace, Name: r.Spec.Controller.Name, UID: types.UID(r.Spec.Controller.UID)}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{Operation: op.DeepCopy(), ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: op.Section, Resource: "stable-key", Operation: *op.DeepCopy()}}}}}
	completion, err := CompletionForReceipt(r, nc, now)
	if err != nil {
		t.Fatal(err)
	}
	r.Status.Completion = completion
	if err := ValidateCompletionReceipt(r, nc, now); err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration.Operation = nil
	if err := ValidateCompletionReceipt(r, nc, now); err != nil {
		t.Fatal("released journal lost receipt", err)
	}
	cases := map[string]func(*ops.ControllerConfigurationReservation, *cvk.NetworkController){
		"missing receipt": func(r *ops.ControllerConfigurationReservation, n *cvk.NetworkController) {
			n.Status.Configuration.ActionApplications = nil
		},
		"replaced controller": func(r *ops.ControllerConfigurationReservation, n *cvk.NetworkController) { n.UID = "replacement" },
		"task mismatch": func(r *ops.ControllerConfigurationReservation, n *cvk.NetworkController) {
			n.Status.Configuration.ActionApplications[0].Operation.TaskID = "other"
		},
		"target mismatch": func(r *ops.ControllerConfigurationReservation, n *cvk.NetworkController) {
			n.Status.Configuration.ActionApplications[0].Operation.TargetSerial = "other"
		},
		"not verified": func(r *ops.ControllerConfigurationReservation, n *cvk.NetworkController) {
			n.Status.Configuration.ActionApplications[0].Operation.Phase = "Accepted"
		},
		"duplicate": func(r *ops.ControllerConfigurationReservation, n *cvk.NetworkController) {
			n.Status.Configuration.ActionApplications = append(n.Status.Configuration.ActionApplications, n.Status.Configuration.ActionApplications[0])
		},
		"before claim": func(r *ops.ControllerConfigurationReservation, n *cvk.NetworkController) {
			v := metav1.NewTime(now.Add(-10 * time.Second))
			n.Status.Configuration.ActionApplications[0].Operation.SubmittedAt = &v
		},
		"future completion": func(r *ops.ControllerConfigurationReservation, n *cvk.NetworkController) {
			r.Status.Completion.VerifiedAt = metav1.NewTime(now.Add(time.Second))
		},
		"no claim": func(r *ops.ControllerConfigurationReservation, n *cvk.NetworkController) { r.Status.Claim = nil },
		"mismatched journal": func(r *ops.ControllerConfigurationReservation, n *cvk.NetworkController) {
			n.Status.Configuration.Operation = op.DeepCopy()
			n.Status.Configuration.Operation.Phase = "Accepted"
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			rr, nn := r.DeepCopy(), nc.DeepCopy()
			mutate(rr, nn)
			if ValidateCompletionReceipt(rr, nn, now) == nil {
				t.Fatal("accepted incomplete or changed receipt")
			}
		})
	}
	deleting := nc.DeepCopy()
	deletionTime := metav1.NewTime(now)
	deleting.DeletionTimestamp = &deletionTime
	if err := ValidateCompletionReceipt(r, deleting, now); err != nil {
		t.Fatal("deletion request stranded verified receipt", err)
	}
	deletingReservation := r.DeepCopy()
	deletingReservation.DeletionTimestamp = &deletionTime
	if err := ValidateCompletionReceipt(deletingReservation, nc, now); err != nil {
		t.Fatal("reservation deletion stranded receipt audit", err)
	}
	if err := ValidateOperationBinding(deletingReservation, op); err == nil {
		t.Fatal("receipt audit weakened dispatch deletion check")
	}
	clone := r.DeepCopy()
	clone.Status.Completion.TaskID = "changed"
	if r.Status.Completion.TaskID != "task" {
		t.Fatal("completion deepcopy aliased")
	}
}
