// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestDiscoveryCredentialConfigCleanupPreparation(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range credentialCleanupPreparationModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "prepare/"+mode, installWirelessFeatureQualificationAPI(t, api, "credential-prepare-"+mode))
		})
	}
}
