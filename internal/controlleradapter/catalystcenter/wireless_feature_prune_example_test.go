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

func TestWirelessFeaturePruneExampleAndScope(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/wireless-feature-prune-apply.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err = yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) {
		t.Fatal("feature prune example unsupported")
	}
	c := &client{}
	source := &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw}
	nc := &cvk.NetworkController{}
	for _, mode := range []string{"known", "secret", "mixed", "cr-delete", "foreign-section"} {
		t.Run(mode, func(t *testing.T) {
			next := config.DeepCopy()
			controller := nc.DeepCopy()
			a := &adapter{client: c}
			if mode == "secret" {
				next.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{}}
			}
			if mode == "mixed" {
				next.Spec.ManagedSections = append(next.Spec.ManagedSections, "wireless")
			}
			if mode == "cr-delete" {
				next.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyDelete
			}
			if mode == "foreign-section" {
				controller.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "wireless", ConfigUID: string(next.UID)}}}
			}
			plan, err := c.compileConfigurationApply(source.raw, config.Spec.ManagedSections, nil)
			if err != nil {
				t.Fatal(err)
			}
			if err = a.bindFeatureCreateOwnership(controller, next, source, &plan); err != nil {
				t.Fatal(err)
			}
			err = a.bindFeaturePrune(controller, next, source, &plan)
			if mode == "known" {
				if err != nil || plan.executor.count() != 0 {
					t.Fatal("explicit empty prune compile failed", err)
				}
			} else if err == nil {
				t.Fatal("unsupported prune scope accepted", mode)
			}
		})
	}
	config.Spec.Mode = configapi.NetworkControllerApplyModeReport
	if supportedConfigReport(&config) {
		t.Fatal("Report mode claimed Delete")
	}
	for _, raw := range []string{`{"catalyst_center":{"feature_templates":{"wireless":{"cleanair":null}}}}`, `{"catalyst_center":{"feature_templates":{"wireless":{"unknown":[]}}}}`, `{"catalyst_center":{"feature_templates":{"wireless":{"rrm_fra":[],"cleanair":[],"extra":[]}}}}`} {
		if _, err := c.compileWirelessFeatureConfiguration([]byte(raw)); err == nil {
			t.Fatal("invalid empty model accepted")
		}
	}
}

func TestWirelessFeatureRecreateExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/wireless-feature-recreate-apply.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err = yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) || config.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyRetain {
		t.Fatal("recreation example needs Retain Apply")
	}
	plan, err := (&client{}).compileConfigurationApply(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections, nil)
	if err != nil || plan.executor.count() != 2 {
		t.Fatal("pinned recreation model invalid", err)
	}
}

func TestWirelessFeatureConfigDeletionExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/wireless-feature-delete-on-config-removal.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err = yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !featureDeletionShape(&config) || supportedConfigurationApply(&config) {
		t.Fatal("cleanup example bypassed enrollment")
	}
	config.Finalizers = []string{featureConfigFinalizer}
	if !supportedConfigurationApply(&config) {
		t.Fatal("enrolled cleanup example unsupported")
	}
	plan, err := (&client{}).compileConfigurationApply(config.Spec.Source.Inline.Raw, config.Spec.ManagedSections, nil)
	if err != nil || plan.executor.count() != 2 {
		t.Fatal("pinned cleanup model invalid", err)
	}
}
