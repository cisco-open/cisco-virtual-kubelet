// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestFabricPortAssignmentExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/fabric-port-assignment-execution.yaml")
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
	devices, channels, err := parseFabricPortAssignmentDeployment(config.Spec.Source.Inline.Raw)
	if err != nil || len(devices) != 1 || len(channels) != 1 || len(channels[0].assignments) != 1 {
		t.Fatal("incomplete executable example", err)
	}
}
