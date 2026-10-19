// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestManagementAddressDeclarationWaitsForExactSettlement(t *testing.T) {
	for _, mode := range []string{"settled", "held", "missing-completion", "missing-manager", "active-session", "device-replaced", "node-replaced", "generation-changed", "wrong-old-address", "wrong-desired-address", "controller-paused"} {
		t.Run(mode, func(t *testing.T) {
			ref, before, op, reservation := managementAddressBaselineFixture(t)
			now := op.SubmittedAt.Time.Add(time.Second)
			op.Phase = "Verified"
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "catc", UID: "controller", Generation: 1}, Spec: cvk.NetworkControllerSpec{Type: TypeName}}
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op.DeepCopy(), ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: "inventory", Resource: op.Resource, Operation: *op.DeepCopy()}}}
			claimed := metav1.NewTime(op.SubmittedAt.Add(-10 * time.Second))
			reservation.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: reservation.Spec.OperationDigest, ClaimedAt: claimed}
			reservation.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Released", GrantedAt: claimed}
			completion, err := contract.CompletionForReceipt(reservation, nc, now)
			if err != nil {
				t.Fatal(err)
			}
			reservation.Status.Completion = completion
			settled := metav1.NewTime(now)
			reservation.Status.ManagerSettledAt = &settled
			device := &cvk.CiscoDevice{ObjectMeta: metav1.ObjectMeta{Namespace: "lab", Name: "switch", UID: "device", Generation: 1}, Spec: cvk.DeviceSpec{Driver: cvk.DeviceDriverXE, Address: before.before, PhysicalIdentity: "serial"}}
			device.Status.NodeIdentity = &cvk.DeviceNodeIdentityStatus{DeviceUID: "device", NodeName: "switch", NodeUID: "node", PhysicalIdentity: "serial"}
			node := &core.Node{ObjectMeta: metav1.ObjectMeta{Name: "switch", UID: "node"}}
			node.Status.NodeInfo.MachineID = "serial"
			switch mode {
			case "held":
				reservation.Status.Grant.Phase = "Held"
			case "missing-completion":
				reservation.Status.Completion = nil
			case "missing-manager":
				reservation.Status.ManagerSettledAt = nil
			case "active-session":
				device.Status.MaintenanceSession = &cvk.DeviceMaintenanceSessionStatus{Phase: cvk.DeviceMaintenanceSessionActive}
			case "device-replaced":
				device.UID = "replacement"
			case "node-replaced":
				node.UID = "replacement"
			case "generation-changed":
				device.Generation++
			case "wrong-old-address":
				device.Spec.Address = "198.51.100.103"
			case "wrong-desired-address":
				ref.IP = "198.51.100.104"
			case "controller-paused":
				nc.Spec.Paused = true
			}
			original := device.DeepCopy()
			proposed, err := planManagementAddressDeclaration(ref, nc, reservation, device, node, now)
			if mode != "settled" {
				if err == nil || proposed != nil {
					t.Fatal("unsafe rollout declaration proposed")
				}
				return
			}
			if err != nil || proposed == nil || proposed.Spec.Address != ref.IP || proposed.UID != device.UID || !reflect.DeepEqual(device, original) {
				t.Fatal("declaration planning lost identity or mutated its input", err)
			}
			proposed.Spec.Address = before.before
			if !reflect.DeepEqual(proposed, device) {
				t.Fatal("planner modified fields other than management address")
			}
		})
	}
}
