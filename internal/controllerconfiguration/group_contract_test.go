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

func groupIdentityFixture() (*cvk.NetworkControllerConfigurationOperation, []GroupTargetIdentity) {
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: "config", ConfigName: "intent", ConfigGeneration: 1, ControllerGeneration: 1, Section: "fabric", Resource: "multicast", ParentID: "fabric", TargetID: "logical-resource", Action: "Update", Phase: "Prepared"}
	targets := []GroupTargetIdentity{
		{InventoryID: "b", PhysicalIdentity: "SERIAL-B", Device: ops.ConfigurationReservationReference{Name: "b", UID: "device-b"}, Node: ops.ConfigurationReservationReference{Name: "b", UID: "node-b"}},
		{InventoryID: "a", PhysicalIdentity: "serial-a", Device: ops.ConfigurationReservationReference{Name: "a", UID: "device-a"}, Node: ops.ConfigurationReservationReference{Name: "a", UID: "node-a"}},
	}
	return op, targets
}
func TestGroupOperationIdentity(t *testing.T) {
	for _, mode := range []string{"phase", "order", "serial-case", "serial-change", "node-change", "device-change", "inventory-change", "source-change", "target-change", "removed-target"} {
		t.Run(mode, func(t *testing.T) {
			op, targets := groupIdentityFixture()
			baseline, err := GroupOperationDigest(op, targets)
			if err != nil {
				t.Fatal(err)
			}
			if targets[0].PhysicalIdentity != "SERIAL-B" || op.Phase != "Prepared" {
				t.Fatal("mutated input")
			}
			switch mode {
			case "phase":
				op.Phase = "Accepted"
				op.TaskID = "task"
				now := metav1.Now()
				op.SubmittedAt = &now
			case "order":
				targets[0], targets[1] = targets[1], targets[0]
			case "serial-case":
				targets[0].PhysicalIdentity = "serial-b"
			case "serial-change":
				targets[0].PhysicalIdentity = "replacement"
			case "node-change":
				targets[0].Node.UID = "replacement"
			case "device-change":
				targets[0].Device.UID = "replacement"
			case "inventory-change":
				targets[0].InventoryID = "replacement"
			case "source-change":
				op.SourceVersion = "replacement"
			case "target-change":
				op.TargetID = "replacement"
			case "removed-target":
				targets = targets[:1]
			}
			current, err := GroupOperationDigest(op, targets)
			if err != nil {
				t.Fatal(err)
			}
			unchanged := mode == "phase" || mode == "order" || mode == "serial-case"
			if (current == baseline) != unchanged {
				t.Fatal("incorrect group identity stability")
			}
			if _, err := OperationDigest(op); err == nil {
				t.Fatal("group operation accepted as v1 physical operation")
			}
		})
	}
}
func TestGroupTargetRefusals(t *testing.T) {
	for _, mode := range []string{"empty", "missing-node", "missing-device", "missing-inventory", "bad-serial", "duplicate-serial", "duplicate-node", "duplicate-device", "duplicate-inventory", "legacy-serial"} {
		t.Run(mode, func(t *testing.T) {
			op, targets := groupIdentityFixture()
			switch mode {
			case "empty":
				targets = nil
			case "missing-node":
				targets[0].Node.UID = ""
			case "missing-device":
				targets[0].Device.UID = ""
			case "missing-inventory":
				targets[0].InventoryID = ""
			case "bad-serial":
				targets[0].PhysicalIdentity = "bad serial"
			case "duplicate-serial":
				targets[0].PhysicalIdentity = "SERIAL-A"
			case "duplicate-node":
				targets[0].Node.UID = targets[1].Node.UID
			case "duplicate-device":
				targets[0].Device.UID = targets[1].Device.UID
			case "duplicate-inventory":
				targets[0].InventoryID = targets[1].InventoryID
			case "legacy-serial":
				op.TargetSerial = "serial-a"
			}
			digest, err := GroupOperationDigest(op, targets)
			if err == nil || digest != "" {
				t.Fatal("invalid group accepted")
			}
		})
	}
}
