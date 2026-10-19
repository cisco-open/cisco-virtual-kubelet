// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"testing"
)

var _ controlleradapter.ConfigurationReservationAdapter = (*adapter)(nil)

func TestConfigurationReservationRuntimeInstallation(t *testing.T) {
	authorize := func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
	a := &adapter{}
	if err := a.ConfigureConfigurationReservations("leases", authorize); err == nil {
		t.Fatal("missing Pod identity accepted")
	}
	a.workerPodName = "worker"
	a.workerPodUID = "pod-uid"
	if err := a.ConfigureConfigurationReservations("leases", nil); err == nil {
		t.Fatal("missing live authorizer accepted")
	}
	if err := a.ConfigureConfigurationReservations("", authorize); err == nil {
		t.Fatal("missing Lease namespace accepted")
	}
	if a.configurationAdmissionReady() {
		t.Fatal("partial setup enabled physical writes")
	}
	if err := a.ConfigureConfigurationReservations("leases", authorize); err != nil {
		t.Fatal(err)
	}
	if !a.configurationAdmissionReady() {
		t.Fatal("valid runtime not installed")
	}
	if err := a.ConfigureConfigurationReservations("other", authorize); err == nil {
		t.Fatal("runtime authority replaced after setup")
	}
	if a.configurationAdmission.leaseNamespace != "leases" {
		t.Fatal("failed replacement modified authority")
	}
}

var _ controlleradapter.ConfigurationGroupReservationAdapter = (*adapter)(nil)

func TestConfigurationGroupRuntimeIsolation(t *testing.T) {
	authorize := func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }
	for _, group := range []bool{false, true} {
		a := &adapter{workerPodName: "worker", workerPodUID: "pod"}
		configure := a.ConfigureConfigurationReservations
		replace := a.ConfigureConfigurationGroupReservations
		if group {
			configure, replace = replace, configure
		}
		if err := configure("leases", authorize); err != nil {
			t.Fatal(err)
		}
		if !a.configurationAdmissionReady() || a.configurationGroupAdmissionReady() != group {
			t.Fatal("runtime protocol selection lost")
		}
		if err := replace("leases", authorize); err == nil {
			t.Fatal("installed protocol changed in place")
		}
		if a.configurationGroupAdmissionReady() != group {
			t.Fatal("failed setup changed installed protocol")
		}
	}
	a := &adapter{}
	if err := a.ConfigureConfigurationGroupReservations("leases", authorize); err == nil || a.configurationAdmission != nil {
		t.Fatal("group installed without worker identity")
	}
}
