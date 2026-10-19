// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestDiscoveryCredentialPublicConfigDeletion(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range credentialPublicDeletionModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "public/"+mode, installWirelessFeatureQualificationAPI(t, api, "credential-public-"+mode))
		})
	}
}

func TestEnvtestDiscoveryCredentialPublicDeletionCohort(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range credentialPublicCohortModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialMultipleScenario(t, mode, installWirelessFeatureQualificationAPI(t, api, "credential-cohort-"+mode))
		})
	}
}

func TestEnvtestDiscoveryCredentialUnsubmittedDeletion(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range credentialUnsubmittedModes {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCleanupObservationScenario(t, "public/"+mode, installWirelessFeatureQualificationAPI(t, api, "credential-unsubmitted-"+mode))
		})
	}
}
