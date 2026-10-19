// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestDiscoveryCredentialRetainedCreateObservation(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"source-lost", "legacy", "drift", "foreign-uuid", "failed-task", "pending-task", "active-job", "journal-changed", "owner-revoked", "scope-version"} {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialRetainedObservationScenario(t, mode, installWirelessFeatureQualificationAPI(t, api, "retained-discovery-"+mode))
		})
	}
}
