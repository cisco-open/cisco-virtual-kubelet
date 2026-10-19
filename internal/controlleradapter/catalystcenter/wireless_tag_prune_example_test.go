// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestWirelessTagPruneExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/wireless-tag-prune-apply.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var c configapi.NetworkControllerConfig
	if err := yaml.Unmarshal(raw, &c); err != nil {
		t.Fatal(err)
	}
	if !wirelessTagComparisonConfig(&c) || !supportedConfigurationApply(&c) || c.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete || c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyRetain {
		t.Fatal("pruning example violates typed scope")
	}
	plan, err := (&client{}).compileConfigurationApply(c.Spec.Source.Inline.Raw, c.Spec.ManagedSections, nil)
	if err != nil || plan.executor.count() != 1 {
		t.Fatal("empty tag/association source did not compile", err)
	}
}
