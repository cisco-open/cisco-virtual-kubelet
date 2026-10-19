//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestDiscoveryInitialNoAuthLifecycle(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range initialNoAuthModes {
		t.Run(mode, func(t *testing.T) {
			initialNoAuthScenario(t, mode, installWirelessFeatureQualificationAPI(t, api, "initial-noauth-"+mode))
		})
	}
}

func TestEnvtestDiscoveryNoAuthRetainLifecycle(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range noAuthRetainModes {
		t.Run(mode, func(t *testing.T) {
			initialNoAuthScenario(t, "retain-"+mode, installWirelessFeatureQualificationAPI(t, api, "noauth-retain-"+mode))
		})
	}
}
