// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
)

func TestConfigurationProtocolOptions(t *testing.T) {
	for _, tc := range []struct {
		protocol string
		managed  bool
		access   string
		valid    bool
	}{
		{contract.Protocol, false, "", true}, {contract.Protocol, true, managedprotocol.WorkerAccessReadOnly, true},
		{contract.GroupReservationProtocol, true, managedprotocol.WorkerAccessReadWrite, true},
		{contract.GroupReservationProtocol, false, managedprotocol.WorkerAccessReadWrite, false},
		{contract.GroupReservationProtocol, true, managedprotocol.WorkerAccessReadOnly, false},
		{contract.GroupReservationProtocol, true, managedprotocol.WorkerAccessDisabled, false},
		{"", true, managedprotocol.WorkerAccessReadWrite, false}, {"unknown", true, managedprotocol.WorkerAccessReadWrite, false},
	} {
		if err := validateConfigurationReservationProtocol(tc.protocol, tc.managed, tc.access); (err == nil) != tc.valid {
			t.Fatalf("protocol=%q managed=%v access=%q err=%v", tc.protocol, tc.managed, tc.access, err)
		}
	}
}
func TestConfigurationProtocolRetainedState(t *testing.T) {
	for _, protocol := range []string{contract.Protocol, contract.GroupReservationProtocol} {
		for _, mode := range []string{"empty", "metadata", "same-journal", "other-journal", "pruned-group", "pruned-port-channel", "pruned-port-assignment", "pruned-vlan-ssid", "pruned-ewlc", "pruned-multicast-vn", "pruned-replication", "same-reservation", "other-reservation", "unknown-reservation"} {
			t.Run(protocol+"/"+mode, func(t *testing.T) {
				scheme := runtime.NewScheme()
				_ = cvk.AddToScheme(scheme)
				_ = ops.AddToScheme(scheme)
				objects := []client.Object{}
				other := contract.Protocol
				if protocol == contract.Protocol {
					other = contract.GroupReservationProtocol
				}
				if mode == "metadata" || mode == "same-journal" || mode == "other-journal" || mode == "pruned-group" || mode == "pruned-port-channel" || mode == "pruned-port-assignment" || mode == "pruned-vlan-ssid" || mode == "pruned-ewlc" || mode == "pruned-multicast-vn" || mode == "pruned-replication" {
					selected := protocol
					if mode == "other-journal" {
						selected = other
					}
					op := &cvk.NetworkControllerConfigurationOperation{TargetSerial: "serial"}
					if selected == contract.GroupReservationProtocol {
						op.TargetSerial = ""
						op.PhysicalGroup = &cvk.NetworkControllerConfigurationGroupIdentity{}
					}
					if mode == "metadata" {
						op = &cvk.NetworkControllerConfigurationOperation{}
					}
					if mode == "pruned-group" {
						op = &cvk.NetworkControllerConfigurationOperation{Section: "fabric", Resource: "fabric-l2-handoff-create:pruned"}
					}
					if mode == "pruned-replication" {
						op = &cvk.NetworkControllerConfigurationOperation{Section: "fabric", Resource: "fabric-replication-update:pruned"}
					}
					if mode == "pruned-multicast-vn" {
						op = &cvk.NetworkControllerConfigurationOperation{Section: "fabric", Resource: "fabric-multicast-vn:pruned"}
					}
					if mode == "pruned-ewlc" {
						op = &cvk.NetworkControllerConfigurationOperation{Section: "inventory", Resource: "fabric-ewlc-update:pruned"}
					}
					if mode == "pruned-vlan-ssid" {
						op = &cvk.NetworkControllerConfigurationOperation{Section: "fabric", Resource: "fabric-vlan-ssid-update:pruned"}
					}
					if mode == "pruned-port-assignment" {
						op = &cvk.NetworkControllerConfigurationOperation{Section: "fabric", Resource: "fabric-port-assignment-create:pruned"}
					}
					if mode == "pruned-port-channel" {
						op = &cvk.NetworkControllerConfigurationOperation{Section: "fabric", Resource: "fabric-port-channel-create:pruned"}
					}
					objects = append(objects, &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: "lab"}, Spec: cvk.NetworkControllerSpec{Type: "catalyst-center"}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{Operation: op}}})
				}
				if mode == "same-reservation" || mode == "other-reservation" || mode == "unknown-reservation" {
					selected := protocol
					if mode == "other-reservation" {
						selected = other
					}
					if mode == "unknown-reservation" {
						selected = "unknown"
					}
					objects = append(objects, &ops.ControllerConfigurationReservation{ObjectMeta: metav1.ObjectMeta{Name: "retained", Namespace: "lab"}, Spec: ops.ControllerConfigurationReservationSpec{Protocol: selected}})
				}
				c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
				err := verifyConfigurationProtocolRetainedState(context.Background(), c, protocol)
				valid := mode != "other-journal" && mode != "other-reservation" && mode != "unknown-reservation" && ((mode != "pruned-group" && mode != "pruned-port-channel" && mode != "pruned-port-assignment" && mode != "pruned-vlan-ssid" && mode != "pruned-ewlc" && mode != "pruned-multicast-vn" && mode != "pruned-replication") || protocol == contract.GroupReservationProtocol)
				if (err == nil) != valid {
					t.Fatalf("retained-state validation: %v", err)
				}
			})
		}
	}
}

func TestConfigurationProtocolRetainedExtranet(t *testing.T) {
	for _, prefix := range []string{"extranet-create:", "extranet-update:", "extranet-update-v2:"} {
		for _, protocol := range []string{contract.Protocol, contract.GroupReservationProtocol} {
			t.Run(prefix+protocol, func(t *testing.T) {
				scheme := runtime.NewScheme()
				_ = cvk.AddToScheme(scheme)
				_ = ops.AddToScheme(scheme)
				// The physical group may have been pruned by an older CRD. The resource
				// family must still prevent falling back to the single-device protocol.
				nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: "lab"}, Spec: cvk.NetworkControllerSpec{Type: "catalyst-center"}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{Operation: &cvk.NetworkControllerConfigurationOperation{Section: "fabric", Resource: prefix + "pruned"}}}}
				c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(nc).Build()
				err := verifyConfigurationProtocolRetainedState(context.Background(), c, protocol)
				if (err == nil) != (protocol == contract.GroupReservationProtocol) {
					t.Fatal("lost retained extranet group classification", err)
				}
			})
		}
	}
}
