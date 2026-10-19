//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestSiteSelectionRetainedOwnership(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	exerciseSitePruneManifestKinds(t, "selection", true, installWirelessFeatureQualificationAPI(t, api, "selected-site-prune"), true)
}
func TestEnvtestSiteSelectionDesign(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"selected-recover-private", "image-selected-keyed-recover-private"} {
		t.Run(mode, func(t *testing.T) {
			testPlannedAPManifestFloorThenPlacement(t, mode, installWirelessFeatureQualificationAPI(t, api, "selected-"+mode))
		})
	}
}

func TestEnvtestMergedSiteDesign(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	testPlannedAPManifestFloorThenPlacement(t, "merged-defaults-image-selected-keyed-recover-private", installWirelessFeatureQualificationAPI(t, api, "merged-design"))
}

func TestEnvtestYAMLSourceSiteDesign(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	testPlannedAPManifestFloorThenPlacement(t, "yaml-merged-defaults-image-selected-keyed-recover-private", installWirelessFeatureQualificationAPI(t, api, "yaml-source-design"))
}
