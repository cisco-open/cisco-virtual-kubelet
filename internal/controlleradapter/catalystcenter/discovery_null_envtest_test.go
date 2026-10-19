//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestEnvtestDiscoveryNullManifestLifecycle(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, combined := range []bool{false, true} {
		t.Run(fmt.Sprint(combined), func(t *testing.T) {
			install := installWirelessFeatureQualificationAPI(t, api, fmt.Sprint("discovery-null-", combined))
			discoveryManifestScenario(t, discoveryCycleScenario{nullable: true, combined: combined, install: func(a *adapter, c *configapi.NetworkControllerConfig) {
				install(a, c)
				var stored configapi.NetworkControllerConfig
				if err := api.Get(context.Background(), ctrlclient.ObjectKeyFromObject(c), &stored); err != nil {
					t.Fatal(err)
				}
				var model map[string]any
				if err := json.Unmarshal(stored.Spec.Source.Inline.Raw, &model); err != nil {
					t.Fatal(err)
				}
				row := model["catalyst_center"].(map[string]any)["inventory"].(map[string]any)["discovery"].([]any)[0].(map[string]any)
				for _, field := range discoveryNullableFields {
					value, present := row[field]
					if !present || value != nil {
						t.Fatal("real API pruned or changed optional null", field)
					}
				}
			}})
		})
	}
}
