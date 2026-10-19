// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestDiscoveryCredentialSourceRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range credentialSourceRecoveryModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialSourceRecoveryScenario(t, mode, installWirelessFeatureQualificationAPI(t, api, "credential-recovery-"+mode))
		})
	}
}
func TestEnvtestDiscoveryCredentialScopePublicationGuard(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	credentialScopePublicationGuard(t, installWirelessFeatureQualificationAPI(t, api, "credential-scope-guard"))
}

func TestEnvtestDiscoveryCredentialScopePublicPublicationRace(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	credentialScopePublicPublicationRace(t, installWirelessFeatureQualificationAPI(t, api, "credential-scope-public-race"))
}

func TestEnvtestDiscoveryCredentialSourceChangeBeforePlanning(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	credentialSourceChangeBeforePlanning(t, installWirelessFeatureQualificationAPI(t, api, "credential-source-planning-race"))
}
