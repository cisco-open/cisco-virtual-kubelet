//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"testing"
)

func TestEnvtestCredentialDefaultsPublicManifestRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	count := 0
	exerciseGlobalCredentialManifest(t, true, func(child *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
		resolveManifestCredentialDefaults(child, a, config)
		count++
		installWirelessFeatureQualificationAPI(child, api, fmt.Sprintf("credential-defaults-%d", count))(a, config)
	})
}

func TestEnvtestCredentialDefaultOverridesPublicManifestRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	count := 0
	exerciseGlobalCredentialManifest(t, true, func(child *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
		resolveManifestCredentialDefaultsWithOverrides(child, a, config, []byte(credentialOverrideFixture))
		count++
		installWirelessFeatureQualificationAPI(child, api, fmt.Sprintf("credential-overrides-%d", count))(a, config)
	})
}
