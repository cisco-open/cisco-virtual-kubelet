// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"os"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"sigs.k8s.io/yaml"
)

func TestProfileSiteTagDependencyExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/profile-site-tag-dependencies.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !wirelessTagComparisonConfig(&config) || !supportedConfigurationApply(&config) || config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete {
		t.Fatal("combined source admission unsupported")
	}
	plan, err := (&client{}).compileConfigurationApply(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections, nil)
	if err != nil || plan.executor.count() != 4 {
		t.Fatal("combined desired model incomplete", err)
	}
}
