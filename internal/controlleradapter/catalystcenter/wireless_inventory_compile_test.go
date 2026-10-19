// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"testing"
)

func TestWirelessInventoryCompilerAssembly(t *testing.T) {
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	source := &configSourceSnapshot{raw: []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"WLC","serial_number":"serial","site":"Global/Lab","state":"REPROVISION","type":"WirelessController","primary_managed_ap_locations":["Global/Lab"],"secondary_managed_ap_locations":[],"anchor_managed_ap_locations":["Global/Lab"]}]}}}`)}
	if _, err := a.compileWirelessInventoryApply(config, source); err == nil {
		t.Fatal("compiler bypassed runtime admission")
	}
	a.workerPodName, a.workerPodUID = "connector", "pod-uid"
	if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	plan, err := a.compileWirelessInventoryApply(config, source)
	if err != nil {
		t.Fatal(err)
	}
	stages, ok := plan.executor.(*wirelessInventoryStages)
	if !ok || stages.validate == nil || len(stages.provisioning) != 1 {
		t.Fatal("wireless stages missing")
	}
	group, ok := stages.prerequisites.(*configurationGroup)
	if !ok || len(group.parts) != 3 || group.parts[0].name != "assignments" {
		t.Fatal("assignment and AP prerequisite ordering lost")
	}
	provision, ok := stages.provisioning[0].(*wirelessProvisionExecutor)
	if !ok || len(provision.apDependencies) != 2 || provision.state != "REPROVISION" {
		t.Fatal("provisioning intent lost")
	}
	if plan.sections[0].ResourceCount != 4 {
		t.Fatal("wrong resource count", plan.sections)
	}
	for i, dependency := range provision.apDependencies {
		if dependency.prefix != group.parts[i+1].name+"/" {
			t.Fatal("receipt namespace differs from journal namespace")
		}
	}
	if _, err := a.client.compileManagedInventoryApply(source.raw); err == nil {
		t.Fatal("legacy compiler unexpectedly enabled wireless execution")
	}
}
