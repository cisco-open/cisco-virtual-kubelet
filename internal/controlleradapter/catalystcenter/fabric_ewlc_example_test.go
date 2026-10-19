// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"os"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"sigs.k8s.io/yaml"
)

func TestFabricEWLCExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/fabric-ewlc-execution.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.UnmarshalStrict(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" {
		t.Fatal("unsupported example")
	}
	if plans, err := (&client{}).compileConfigReports(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections); err != nil || len(plans) != 1 || plans[0].name != "inventory" || plans[0].count != 1 {
		t.Fatal("unsupported report example", err)
	}
	intents, err := parseFabricEWLCIntents(config.Spec.Source.Inline.Raw)
	if err != nil || len(intents) != 1 {
		t.Fatal(intents, err)
	}
	if !intents[0].settings.enabled || !intents[0].settings.rolling || intents[0].settings.percentage != 25 || intents[0].device.inventory.reference.Serial != "FOC12345678" || intents[0].locations == nil || len(intents[0].locations.primary) != 1 {
		t.Fatal("incomplete example")
	}
}
