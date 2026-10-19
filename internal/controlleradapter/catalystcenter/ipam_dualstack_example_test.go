// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestIPAMDualStackExampleCompilesApplyAndReport(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/ipam-dualstack-apply.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) {
		t.Fatal("example not admitted for ordinary Apply")
	}
	source := config.Spec.Source.Inline.Raw
	intent, err := parseNetworkReportIntent(source)
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.pools) != 2 || len(intent.reservations) != 1 {
		t.Fatal("same-name dual-stack reservation was not merged")
	}
	reservation := intent.reservations[0]
	if reservation.V4 == nil || reservation.V6 == nil || reservation.V6.SLAAC == nil || !*reservation.V6.SLAAC || reservation.V4.GlobalPoolID == reservation.V6.GlobalPoolID || reservation.site != "Global/CVK-Lab" {
		t.Fatal("family/parent/site binding lost")
	}
	c := &client{}
	plan, err := c.compileConfigurationApply(source, config.Spec.ManagedSections, nil)
	if err != nil || plan.section != "network_settings" || plan.executor.count() != 3 {
		t.Fatal("Apply compilation", err)
	}
	reports, err := c.compileConfigReports(source, config.Spec.ManagedSections)
	if err != nil || len(reports) != 1 {
		t.Fatal("Report compilation", err)
	}
}
