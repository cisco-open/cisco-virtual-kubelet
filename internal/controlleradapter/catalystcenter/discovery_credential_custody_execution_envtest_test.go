//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestDiscoveryCredentialCustodyExecution(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range credentialCustodyExecutionModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "handoff-observe/custody-execute-"+mode, installWirelessFeatureQualificationAPI(t, api, "credential-custody-exec-"+mode))
		})
	}
}
