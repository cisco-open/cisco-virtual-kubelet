// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"os"
	"sigs.k8s.io/yaml"
	"testing"
)

func TestMulticastVNExecutionExample(t *testing.T)          { testMulticastExecutionExample(t, false) }
func TestMulticastReplicationExecutionExample(t *testing.T) { testMulticastExecutionExample(t, true) }
func testMulticastExecutionExample(t *testing.T, replication bool) {
	path := "../../../examples/configs/catalyst-center/multicast-vn-execution.yaml"
	if replication {
		path = "../../../examples/configs/catalyst-center/multicast-replication-execution.yaml"
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.UnmarshalStrict(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" {
		t.Fatal("unsupported example")
	}
	plan, err := (&client{}).compileFabricConfiguration(config.Spec.Source.Inline.Raw)
	if err != nil {
		t.Fatal(err)
	}
	multicast, ok := plan.configurationExecutor.(*fabricMulticastConfiguration)
	if !ok || len(multicast.desired) != 1 {
		t.Fatal("missing multicast intent")
	}
	site := multicast.desired[0]
	if site.path != "Global/Lab" || len(site.intent.networks) != 1 || site.intent.networks[0].name != "Corp" || site.intent.networks[0].pool != "access" || len(site.intent.networks[0].rps) != 1 || site.intent.networks[0].rps[0].devices[0] != "border-a" || site.intent.replication != map[bool]string{false: "", true: "NATIVE_MULTICAST"}[replication] || site.intent.wireless != nil {
		t.Fatal("unexpected multicast projection", site)
	}
}
