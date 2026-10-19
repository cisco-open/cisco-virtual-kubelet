//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"fmt"
	"testing"
)

func TestEnvtestSwitchingProfileTaskRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, update := range []bool{false, true} {
		for _, mode := range switchingProfileRecoveryModes {
			name := fmt.Sprintf("sp-%t-%s", update, mode)
			t.Run(name, func(t *testing.T) {
				exerciseSwitchingProfileRecovery(t, update, mode, installWirelessFeatureQualificationAPI(t, api, name))
			})
		}
	}
	for _, mode := range []string{"profile-replaced", "mid-tree-profile"} {
		t.Run(mode, func(t *testing.T) {
			exerciseSwitchingProfileRecovery(t, true, mode, installWirelessFeatureQualificationAPI(t, api, "sp-"+mode))
		})
	}
}
