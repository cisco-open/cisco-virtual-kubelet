// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestPlannedAPFloorHandoff(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range floorHandoffModes {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, mode, installWirelessFeatureQualificationAPI(t, api, "handoff-"+mode))
		})
	}
}
