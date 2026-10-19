// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestFabricWirelessMulticastExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/fabric-wireless-multicast-execution.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.UnmarshalStrict(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) {
		t.Fatal("unsupported example")
	}
	plan, err := (&client{}).compileFabricConfiguration(config.Spec.Source.Inline.Raw)
	if err != nil {
		t.Fatal(err)
	}
	multicast, ok := plan.configurationExecutor.(*fabricMulticastConfiguration)
	if !ok || len(multicast.desired) != 1 || multicast.desired[0].intent.wireless == nil || !*multicast.desired[0].intent.wireless || plan.count() != 2 {
		t.Fatal("incomplete multicast example")
	}
}
