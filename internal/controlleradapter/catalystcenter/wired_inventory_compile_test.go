// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"strings"
	"testing"
)

func wiredInventoryModel() string {
	raw := strings.Replace(fabricEdgeIntentModel, `,"fabric_site":"Global/Lab"`, "", 1)
	return strings.Replace(raw, `,"fabric_roles":["EDGE_NODE"]`, "", 1)
}
func TestWiredInventoryCompiler(t *testing.T) {
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	raw := wiredInventoryModel()
	source := &configSourceSnapshot{raw: []byte(raw)}
	if _, err := a.compileWiredInventoryApply(config, source); err == nil {
		t.Fatal("missing admission accepted")
	}
	a.workerPodName, a.workerPodUID = "connector", "pod-uid"
	if err := a.ConfigureConfigurationReservations("leases", func(context.Context, *ops.ControllerConfigurationReservation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	plan, err := a.compileWiredInventoryApply(config, source)
	if err != nil {
		t.Fatal(err)
	}
	stages, ok := plan.executor.(*wiredInventoryStages)
	if !ok || len(stages.provisioning) != 1 || stages.validate == nil || plan.sections[0].ResourceCount != 2 {
		t.Fatal("incorrect staged compiler", plan)
	}
	if stages.provisioning[0].state != "PROVISION" || stages.provisioning[0].desired.reference.Serial != "FOC12345678" {
		t.Fatal("lost provisioning intent")
	}
	if _, err := a.client.compileManagedInventoryApply([]byte(raw)); err == nil {
		t.Fatal("legacy compiler silently enables provisioning")
	}
	for _, replacement := range [][2]string{
		{`"PROVISION"`, `"ASSIGN"`}, {`"PROVISION"`, `"BAD"`}, {`"name":"edge"`, `"name":"edge","fabric_roles":["EDGE_NODE"]`},
		{`"name":"edge"`, `"name":"edge","type":"WirelessController"`}, {`"name":"edge"`, `"name":"edge","clean_up_config":true`},
	} {
		if _, _, err := parseWiredInventoryIntent([]byte(strings.Replace(raw, replacement[0], replacement[1], 1))); err == nil {
			t.Fatal("unsupported intent discarded", replacement)
		}
	}
}
func TestWiredInventoryBindings(t *testing.T) {
	e, _ := newWiredExecutorFixture(t)
	intent := inventoryIntent{devices: []inventoryDeviceIntent{e.desired}}
	states := map[string]string{"edge": "REPROVISION"}
	bindings, err := e.adapter.client.wiredInventoryBindings(context.Background(), intent, states)
	if err != nil || len(bindings) != 1 || bindings["edge"].serial != "foc12345678" {
		t.Fatal(bindings, err)
	}
	alias := e.desired
	alias.reference.Name = "alias"
	intent.devices = append(intent.devices, alias)
	if _, err := e.adapter.client.wiredInventoryBindings(context.Background(), intent, states); err == nil {
		t.Fatal("two model aliases target one physical device")
	}
	intent.devices = intent.devices[:1]
	intent.devices[0].site = "Global/Other"
	if _, err := e.adapter.client.wiredInventoryBindings(context.Background(), intent, states); err == nil {
		t.Fatal("missing placement accepted")
	}
}
