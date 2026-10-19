// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func managementAddressBaselineFixture(t *testing.T) (configurationDeviceReference, *managementAddressChange, *cvk.NetworkControllerConfigurationOperation, *ops.ControllerConfigurationReservation) {
	t.Helper()
	ref := configurationDeviceReference{Name: "switch", IP: "198.51.100.101"}
	before := &managementAddressChange{device: Device{ID: "native-id", Serial: "serial"}, before: "198.51.100.100", after: ref.IP}
	stamp := metav1.Now()
	op := &cvk.NetworkControllerConfigurationOperation{ID: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ConfigName: "intent", ConfigUID: "config", ConfigGeneration: 1, ControllerGeneration: 1, Section: "inventory", Resource: managementAddressResource(ref, before, "serial"), Action: "Update", TargetID: "native-id", TargetSerial: "serial", Phase: "Accepted", TaskID: "native-task", SubmittedAt: &stamp}
	digest, err := contract.OperationDigest(op)
	if err != nil {
		t.Fatal(err)
	}
	name, err := contract.ReservationName("controller", op.ID)
	if err != nil {
		t.Fatal(err)
	}
	r := &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: name, UID: "reservation"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: contract.Protocol, OperationID: op.ID, OperationDigest: digest, Config: ops.ConfigurationReservationReference{Name: op.ConfigName, UID: op.ConfigUID}, Controller: ops.ConfigurationReservationReference{Name: "catc", UID: "controller"}, ConfigGeneration: 1, ControllerGeneration: 1, InventoryID: op.TargetID, PhysicalIdentity: "serial", ManagementAddress: before.before, Device: ops.ConfigurationReservationReference{Name: "switch", UID: "device"}, DeviceGeneration: 1, Node: ops.ConfigurationReservationReference{Name: "switch", UID: "node"}}}
	return ref, before, op, r
}

func TestManagementAddressRetainedBaselineSurvivesNativeEdit(t *testing.T) {
	ref, before, op, r := managementAddressBaselineFixture(t)
	stamp := metav1.Now()
	for _, mode := range []string{"accepted", "unknown-with-task", "verifying", "no-task", "wrong-namespace", "wrong-old-address", "wrong-new-address", "wrong-serial", "wrong-device", "wrong-digest", "missing-reservation-uid", "prepared", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			operation, reservation, source := op.DeepCopy(), r.DeepCopy(), ref
			switch mode {
			case "unknown-with-task":
				operation.Phase = "Unknown"
			case "verifying":
				operation.Phase = "Verifying"
			case "no-task":
				operation.TaskID = ""
			case "wrong-namespace":
				reservation.Namespace = "other"
			case "wrong-old-address":
				reservation.Spec.ManagementAddress = "198.51.100.103"
			case "wrong-new-address":
				source.IP = "198.51.100.104"
			case "wrong-serial":
				reservation.Spec.PhysicalIdentity = "other"
			case "wrong-device":
				reservation.Spec.InventoryID = "replacement"
			case "wrong-digest":
				reservation.Spec.OperationDigest = "wrong"
			case "missing-reservation-uid":
				reservation.UID = ""
			case "prepared":
				operation.Phase = "Prepared"
			case "cancelled":
				operation.CancellationRequestedAt = &stamp
			}
			// Native inventory may already contain the new address. Recovery
			// receives no inventory projection from which to guess the old one.
			baseline, err := retainedManagementAddressBaseline("lab", source, operation, reservation)
			valid := mode == "accepted" || mode == "unknown-with-task" || mode == "verifying"
			if !valid {
				if err == nil || baseline != nil {
					t.Fatal("uncorrelated retained baseline accepted")
				}
				return
			}
			if err != nil || baseline == nil || baseline.before != before.before || baseline.after != ref.IP || baseline.device.ID != op.TargetID || baseline.device.Serial != op.TargetSerial {
				t.Fatal("retained baseline lost", err)
			}
			baseline.device.ManagementIP = "changed"
			if reservation.Spec.ManagementAddress != before.before {
				t.Fatal("returned baseline aliases reservation")
			}
		})
	}
}
