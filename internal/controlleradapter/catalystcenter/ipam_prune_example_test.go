// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"os"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/yaml"
)

func TestIPAMPruneExampleAndExplicitEmptyModel(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/ipam-prune-apply.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) {
		t.Fatal("owned IPAM prune policy not supported")
	}
	a := &adapter{client: &client{}}
	source := &configSourceSnapshot{raw: config.Spec.Source.Inline.Raw}
	plan, err := a.compileIPAMPruneApply(&cvk.NetworkController{}, &config, source)
	if err != nil || plan.section != "network_settings" || plan.executor.count() != 0 || !plan.executor.(*ipamConfigurationExecutor).prune {
		t.Fatal("explicit empty prune example", err)
	}
	config.Spec.Mode = configapi.NetworkControllerApplyModeReport
	if supportedConfigReport(&config) {
		t.Fatal("Report claimed Delete semantics")
	}
	config.Spec.PrunePolicy = configapi.NetworkControllerRetentionPolicyRetain
	if !supportedConfigReport(&config) {
		t.Fatal("Retain Report rejected")
	}
	reports, err := a.client.compileConfigReports(source.raw, config.Spec.ManagedSections)
	if err != nil || len(reports) != 1 {
		t.Fatal("empty-pool Report compilation", err)
	}
	for _, model := range []string{`{"catalyst_center":{"network_settings":{"ip_pools":null}}}`, `{"catalyst_center":{"network_settings":{"ip_pools":[],"unsupported":{}}}}`, `{"catalyst_center":{"network_settings":{"ip_pools":[]},"sites":{"areas":[{"name":"Lab","parent_name":"Global","ip_pools_reservations":["missing"]}]}}}`} {
		if _, err := parseNetworkReportIntent([]byte(model)); err == nil {
			t.Fatal("empty intent discarded invalid source")
		}
	}
}

func TestIPAMDefaultEmptySettingsCompilation(t *testing.T) {
	a := &adapter{client: &client{}}
	for _, model := range []string{
		`{"catalyst_center":{"network_settings":{}}}`,
		`{"catalyst_center":{"network_settings":{},"sites":{"areas":[{"name":"Lab","parent_name":"Global","ip_pools_reservations":[]}]}}}`,
	} {
		intent, err := parseNetworkReportIntent([]byte(model))
		if err != nil || len(intent.pools)+len(intent.reservations)+len(intent.bindings)+len(intent.credentials)+len(intent.assignments) != 0 {
			t.Fatal("omitted pools did not default to empty", err)
		}
		config := &configapi.NetworkControllerConfig{}
		config.Spec.ManagedSections = []string{"network_settings"}
		source := &configSourceSnapshot{raw: []byte(model)}
		for _, policy := range []configapi.NetworkControllerRetentionPolicy{configapi.NetworkControllerRetentionPolicyRetain, configapi.NetworkControllerRetentionPolicyDelete} {
			config.Spec.PrunePolicy = policy
			plan, err := a.compileNetworkSettingsApply(&cvk.NetworkController{}, config, source)
			if err != nil || plan.executor.count() != 0 {
				t.Fatal("default-empty Apply compilation", policy, err)
			}
		}
		reports, err := a.client.compileConfigReports(source.raw, config.Spec.ManagedSections)
		if err != nil || len(reports) != 1 || reports[0].count != 0 {
			t.Fatal("default-empty Report compilation", err)
		}
	}
	for _, model := range []string{
		`{"catalyst_center":{"network_settings":null}}`,
		`{"catalyst_center":{"network_settings":[]}}`,
		`{"catalyst_center":{"network_settings":{"unknown":[]}}}`,
		`{"catalyst_center":{"network_settings":{},"sites":{"areas":[{"name":"Lab","parent_name":"Global","ip_pools_reservations":["undefined"]}]}}}`,
		`{"catalyst_center":{"network_settings":{},"sites":{"areas":[{"name":"Lab","parent_name":"Global","network_settings":"undefined"}]}}}`,
	} {
		if _, err := parseNetworkReportIntent([]byte(model)); err == nil {
			t.Fatal("default-empty normalization discarded invalid model")
		}
		if _, err := parseIPAMPruneIntent([]byte(model)); err == nil {
			t.Fatal("default-empty pruning discarded invalid model")
		}
	}
}
