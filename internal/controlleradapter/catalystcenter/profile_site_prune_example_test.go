// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestProfileSitePruneExampleScope(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/profile-site-prune.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) {
		t.Fatal("association prune example unsupported")
	}
	c := &client{}
	a := &adapter{client: c}
	source := &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw}
	for _, mode := range []string{"empty", "secret", "mixed", "cr-delete"} {
		t.Run(mode, func(t *testing.T) {
			next := config.DeepCopy()
			switch mode {
			case "secret":
				next.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{}}
			case "mixed":
				next.Spec.ManagedSections = append(next.Spec.ManagedSections, "wireless")
			case "cr-delete":
				next.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			}
			plan, err := c.compileConfigurationApply(source.raw, config.Spec.ManagedSections, nil)
			if err != nil {
				t.Fatal(err)
			}
			err = a.bindProfileSitePrune(&cvk.NetworkController{}, next, source, &plan)
			if mode == "empty" {
				if err != nil || plan.executor.count() != 0 {
					t.Fatal("explicit empty association scope rejected", err)
				}
			} else if err == nil {
				t.Fatal("unsupported deletion scope accepted")
			}
		})
	}
	config.Spec.Mode = configapi.NetworkControllerApplyModeReport
	if supportedConfigReport(&config) || supportedConfigurationApply(&config) {
		t.Fatal("Report widened to delete scope")
	}
	for _, raw := range []string{`{"catalyst_center":{"network_profiles":{"switching":null}}}`, `{"catalyst_center":{"network_profiles":{"wireless":null}}}`, `{"catalyst_center":{"network_profiles":{}}}`, `{"catalyst_center":{"network_profiles":{"wireless":[],"unknown":[]}}}`} {
		if _, err := c.compileNetworkProfileConfiguration([]byte(raw)); err == nil {
			t.Fatal("unsupported empty/null selection accepted")
		}
	}
}
