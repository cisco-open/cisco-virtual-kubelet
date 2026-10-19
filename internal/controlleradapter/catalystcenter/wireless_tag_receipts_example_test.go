// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestWirelessTagBaselineExampleScope(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/wireless-tag-baselines-apply.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !wirelessTagComparisonConfig(&config) || !supportedConfigurationApply(&config) {
		t.Fatal("private baseline example unsupported")
	}
	plan, err := (&client{}).compileConfigurationApply(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections, nil)
	if err != nil || plan.executor.count() != 4 {
		t.Fatal("complete pinned model not compiled", err)
	}
	for _, mode := range []string{"value-use", "wrong-section", "wrong-path", "multiple", "mixed", "config-delete", "report"} {
		t.Run(mode, func(t *testing.T) {
			c := config.DeepCopy()
			switch mode {
			case "value-use":
				c.Spec.SecretRefs[0].Use = "Value"
			case "wrong-section":
				c.Spec.SecretRefs[0].Section = "wireless"
			case "wrong-path":
				c.Spec.SecretRefs[0].Path = "/wireless"
			case "multiple":
				c.Spec.SecretRefs = append(c.Spec.SecretRefs, c.Spec.SecretRefs[0])
			case "mixed":
				c.Spec.ManagedSections = append(c.Spec.ManagedSections, "wireless")
			case "config-delete":
				c.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			case "report":
				c.Spec.Mode = configapi.NetworkControllerApplyModeReport
			}
			if supportedConfigurationApply(c) {
				t.Fatal("unsupported key/admission scope accepted")
			}
		})
	}
}
