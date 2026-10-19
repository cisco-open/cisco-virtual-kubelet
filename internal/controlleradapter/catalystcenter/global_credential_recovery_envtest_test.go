//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"testing"
)

func TestEnvtestGlobalCredentialKnownTaskRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	count := 0
	exerciseGlobalCredentialManifest(t, true, func(child *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
		count++
		installWirelessFeatureQualificationAPI(child, api, fmt.Sprintf("credential-recovery-%02d", count))(a, config)
	})
}

func TestEnvtestGlobalCredentialMetadataKnownTaskRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	exerciseMetadataCredentialManifest(t, true, func(child *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
		installWirelessFeatureQualificationAPI(child, api, "metadata-task-recovery")(a, config)
	})
}
