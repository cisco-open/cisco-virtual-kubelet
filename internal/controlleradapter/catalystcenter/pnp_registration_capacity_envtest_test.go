//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestPnPRegistrationCapacity(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range pnpCapacityModes {
		t.Run(mode, func(t *testing.T) {
			exercisePnPRegistrationCapacity(t, mode, installWirelessFeatureQualificationAPI(t, api, "pnp-capacity-"+mode))
		})
	}
}

func TestEnvtestPnPPrivateRegistrationCapacity(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range pnpCapacityModes {
		t.Run(mode, func(t *testing.T) {
			exercisePnPRegistrationCapacityEntry(t, mode, true, installWirelessFeatureQualificationAPI(t, api, "pnp-private-capacity-"+mode))
		})
	}
}
