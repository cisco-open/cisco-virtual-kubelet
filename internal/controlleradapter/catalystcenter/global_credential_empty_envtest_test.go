//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"testing"
)

func TestEnvtestGlobalCredentialEmptyRetainAndRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	count := 0
	exerciseEmptyCredentialRetain(t, func(child *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
		count++
		installWirelessFeatureQualificationAPI(child, api, fmt.Sprintf("empty-credential-%d", count))(a, config)
	})
}
