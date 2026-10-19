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

func TestValidateOperationBinding(t *testing.T) {
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: "config-uid", ConfigName: "intent", ConfigGeneration: 2, ControllerGeneration: 3, Section: "inventory", Resource: "resource", TargetID: "inventory-uuid", TargetSerial: "serial", Action: "Update", Phase: "Prepared"}
	digest, err := OperationDigest(op)
	if err != nil {
		t.Fatal(err)
	}
	name, _ := ReservationName("controller-uid", op.ID)
	r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: name, UID: "reservation-uid"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: Protocol, Controller: ops.ConfigurationReservationReference{UID: "controller-uid"}, Config: ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}, ConfigGeneration: op.ConfigGeneration, ControllerGeneration: op.ControllerGeneration, OperationID: op.ID, OperationDigest: digest, InventoryID: op.TargetID}}
	if err := ValidateOperationBinding(r, op); err != nil {
		t.Fatal(err)
	}
	for _, phase := range []string{"Prepared", "Submitting", "Accepted", "Verifying", "Verified"} {
		copy := op.DeepCopy()
		copy.Phase = phase
		if err := ValidateOperationBinding(r, copy); err != nil {
			t.Fatal(phase, err)
		}
	}
	changes := map[string]func(*cvk.NetworkControllerConfigurationOperation){
		"config revision":     func(o *cvk.NetworkControllerConfigurationOperation) { o.ConfigGeneration++ },
		"controller revision": func(o *cvk.NetworkControllerConfigurationOperation) { o.ControllerGeneration++ },
		"source":              func(o *cvk.NetworkControllerConfigurationOperation) { o.SourceVersion = "changed" },
		"target":              func(o *cvk.NetworkControllerConfigurationOperation) { o.TargetID = "replacement" },
		"serial":              func(o *cvk.NetworkControllerConfigurationOperation) { o.TargetSerial = "replacement" },
		"action":              func(o *cvk.NetworkControllerConfigurationOperation) { o.Action = "Delete" },
		"resource":            func(o *cvk.NetworkControllerConfigurationOperation) { o.Resource = "other" },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			copy := op.DeepCopy()
			change(copy)
			if ValidateOperationBinding(r, copy) == nil {
				t.Fatal("accepted different operation")
			}
		})
	}
	copy := r.DeepCopy()
	copy.Spec.InventoryID = "other"
	if ValidateOperationBinding(copy, op) == nil {
		t.Fatal("accepted mismatched explicit target")
	}
	if ValidateOperationBinding(nil, op) == nil || ValidateOperationBinding(r, nil) == nil {
		t.Fatal("accepted missing binding")
	}
}
