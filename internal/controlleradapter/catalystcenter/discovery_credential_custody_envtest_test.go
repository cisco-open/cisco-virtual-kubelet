//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestDiscoveryCredentialCustodyPublication(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range credentialCustodyModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "handoff-observe/custody-"+mode, installWirelessFeatureQualificationAPI(t, api, "credential-custody-"+mode))
		})
	}
}
