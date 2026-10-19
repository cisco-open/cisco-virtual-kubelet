//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestSiteDefaultsDesignManifest(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"defaults-recover-private", "image-defaults-recover-private", "image-defaults-keyed-recover-private"} {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, mode, installWirelessFeatureQualificationAPI(t, api, "defaults-design-"+mode))
		})
	}
}
