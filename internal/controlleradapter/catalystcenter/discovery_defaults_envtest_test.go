//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestDiscoveryDefaultsManifestLifecycle(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	discoveryManifestScenario(t, discoveryCycleScenario{replacement: true, defaults: true, install: installWirelessFeatureQualificationAPI(t, api, "discovery-defaults")})
}
