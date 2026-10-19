// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package main

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/configurationreservation"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/maintenance"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/rest"
	"testing"
)

func TestConfigurationReservationRecoveryIsolation(t *testing.T) {
	fresh := func() configReconcilerOptions {
		return configReconcilerOptions{
			Spec: &cvk.DeviceSpec{Driver: cvk.DeviceDriverXE}, ManagedTopology: true,
			DeviceNamespace: "lab", DeviceUID: "device-uid", WorkerPodName: "worker", WorkerPodUID: "pod-uid",
			Maintenance: &maintenance.Coordinator{ManagedTopology: true, WorkerMode: managedprotocol.WorkerModeNetworkManagement,
				Namespace: "lab", DeviceName: "switch", DeviceUID: "device-uid", WorkerPodName: "worker", WorkerPodUID: "pod-uid"},
		}
	}
	tests := []struct {
		name   string
		change func(*configReconcilerOptions)
		want   bool
	}{
		{"dedicated XE without gNOI mutation flags", func(o *configReconcilerOptions) {}, true},
		{"standalone", func(o *configReconcilerOptions) { o.ManagedTopology = false }, false},
		{"read only", func(o *configReconcilerOptions) { o.ReadOnly = true }, false},
		{"NXOS", func(o *configReconcilerOptions) { o.Spec.Driver = cvk.DeviceDriverNXOS }, false},
		{"missing spec", func(o *configReconcilerOptions) { o.Spec = nil }, false},
		{"missing maintenance", func(o *configReconcilerOptions) { o.Maintenance = nil }, false},
		{"legacy maintenance", func(o *configReconcilerOptions) { o.Maintenance.ManagedTopology = false }, false},
		{"app worker", func(o *configReconcilerOptions) { o.Maintenance.WorkerMode = managedprotocol.WorkerModeAppHosting }, false},
		{"foreign namespace", func(o *configReconcilerOptions) { o.Maintenance.Namespace = "other" }, false},
		{"foreign device", func(o *configReconcilerOptions) { o.Maintenance.DeviceName = "other" }, false},
		{"replaced device", func(o *configReconcilerOptions) { o.Maintenance.DeviceUID = "other" }, false},
		{"foreign pod", func(o *configReconcilerOptions) { o.Maintenance.WorkerPodName = "other" }, false},
		{"replaced pod", func(o *configReconcilerOptions) { o.Maintenance.WorkerPodUID = "other" }, false},
		{"missing namespace", func(o *configReconcilerOptions) { o.DeviceNamespace = ""; o.Maintenance.Namespace = "" }, false},
		{"missing device UID", func(o *configReconcilerOptions) { o.DeviceUID = ""; o.Maintenance.DeviceUID = "" }, false},
		{"missing pod name", func(o *configReconcilerOptions) { o.WorkerPodName = ""; o.Maintenance.WorkerPodName = "" }, false},
		{"missing pod UID", func(o *configReconcilerOptions) { o.WorkerPodUID = ""; o.Maintenance.WorkerPodUID = "" }, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := fresh()
			tt.change(&o)
			if got := o.configurationReservationRecoveryEnabled("switch", o.WorkerPodUID); got != tt.want {
				t.Fatalf("enabled=%v want %v", got, tt.want)
			}
		})
	}
}

func TestConfigurationReservationPreparationSetup(t *testing.T) {
	binding := contract.RuntimeAdmissionBinding{Protocol: contract.Protocol, ManagerUsername: "system:serviceaccount:system:manager", AdmissionPrefix: "cvk", PolicyNamespace: "system", PolicyName: "policy", LedgerName: "ledger", AppHostingServiceAccount: "app", NetworkManagementServiceAccount: "network", LeaseNamespace: "leases"}
	encoded, err := binding.Encode()
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"current", "group", "legacy", "invalid binding", "no strict inventory", "readonly", "wrong Lease namespace"} {
		t.Run(scenario, func(t *testing.T) {
			opts := configReconcilerOptions{Spec: &cvk.DeviceSpec{Driver: cvk.DeviceDriverXE}, ManagedTopology: true, DeviceNamespace: "lab", DeviceUID: "device-uid", WorkerPodName: "worker", WorkerPodUID: "pod-uid", Maintenance: &maintenance.Coordinator{Namespace: "lab", DeviceName: "switch", DeviceUID: "device-uid", ManagedTopology: true, WorkerMode: managedprotocol.WorkerModeNetworkManagement, WorkerPodName: "worker", WorkerPodUID: "pod-uid"}, DrainDevicePodLister: func(context.Context) ([]*corev1.Pod, error) { return nil, nil }}
			r := &configurationreservation.Reconciler{DeviceName: "switch", PodUID: "pod-uid", Leaser: &engine.FamilyLeaser{Namespace: "leases"}}
			raw := encoded
			switch scenario {
			case "group":
				groupBinding := binding
				groupBinding.Protocol = contract.GroupReservationProtocol
				raw, err = groupBinding.Encode()
				if err != nil {
					t.Fatal(err)
				}
			case "legacy":
				raw = ""
			case "invalid binding":
				raw = "{}"
			case "no strict inventory":
				opts.DrainDevicePodLister = nil
			case "readonly":
				opts.ReadOnly = true
			case "wrong Lease namespace":
				r.Leaser.Namespace = "wrong"
			}
			err := configureConfigurationReservationPreparation(r, &rest.Config{Host: "https://kubernetes.invalid"}, opts, raw)
			wantErr := scenario == "invalid binding" || scenario == "readonly" || scenario == "wrong Lease namespace"
			if (err != nil) != wantErr {
				t.Fatalf("err=%v", err)
			}
			active := r.Authorize != nil && r.Prepare != nil && r.StrictDevicePodLister != nil && r.ControllerType == "catalyst-center"
			if active != (scenario == "current" || scenario == "group") {
				t.Fatalf("active=%v", active)
			}
		})
	}
}
