// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"strings"
	"testing"
)

func TestOperationBindingSurvivesPhaseButNotIntentChanges(t *testing.T) {
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: "config", ConfigName: "intent", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: "resource", TargetID: "device", TargetSerial: "serial", Action: "Update", Phase: "Prepared"}
	digest, err := OperationDigest(op)
	if err != nil {
		t.Fatal(err)
	}
	copy := op.DeepCopy()
	copy.Phase = "Accepted"
	copy.TaskID = "task"
	now := metav1.Now()
	copy.SubmittedAt = &now
	if got, err := OperationDigest(copy); err != nil || got != digest {
		t.Fatal("phase changed identity", err)
	}
	copy.SourceVersion = "new"
	if got, err := OperationDigest(copy); err != nil || got == digest {
		t.Fatal("source change did not rebind", err)
	}
	copy = op.DeepCopy()
	copy.TargetSerial = ""
	if _, err := OperationDigest(copy); err == nil {
		t.Fatal("pruned physical binding accepted")
	}
	if op.Phase != "Prepared" || op.TaskID != "" {
		t.Fatal("mutated input")
	}
}
func TestReservationIdentityAndDeepCopy(t *testing.T) {
	name, err := ReservationName("controller", strings.Repeat("a", 32))
	if err != nil {
		t.Fatal(err)
	}
	other, _ := ReservationName("replacement", strings.Repeat("a", 32))
	if name == other {
		t.Fatal("controller incarnation ignored")
	}
	r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: name, UID: "reservation"}, Status: ops.ControllerConfigurationReservationStatus{Grant: &ops.ConfigurationReservationGrant{Phase: "Held"}, Claim: &ops.ConfigurationReservationClaim{OperationID: strings.Repeat("a", 32)}}}
	holder, err := Holder("lab", r)
	if err != nil || holder != "controller-configuration/reservation" {
		t.Fatal(holder, err)
	}
	if _, err := Holder("other", r); err == nil {
		t.Fatal("cross namespace holder")
	}
	clone := r.DeepCopy()
	clone.Status.Grant.Phase = "Released"
	clone.Status.Claim.OperationID = "changed"
	if r.Status.Grant.Phase != "Held" || r.Status.Claim.OperationID != strings.Repeat("a", 32) {
		t.Fatal("aliased reservation status")
	}
}
