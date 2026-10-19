// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"os"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"sigs.k8s.io/yaml"
)

func TestWirelessTagMembershipExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/wireless-tag-membership-apply.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !wirelessTagComparisonConfig(&config) || !supportedConfigurationApply(&config) || config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete || config.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyRetain {
		t.Fatal("unsupported removal example")
	}
	plan, err := (&client{}).compileConfigurationApply(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections, nil)
	if err != nil || plan.executor.count() != 5 {
		t.Fatal("complete source did not compile", err)
	}
}
