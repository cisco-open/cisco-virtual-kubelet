//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestSitePruneManifestLifecycle(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range sitePruneManifestModes {
		t.Run(mode, func(t *testing.T) {
			exerciseSitePruneManifest(t, mode, true, installWirelessFeatureQualificationAPI(t, api, "site-prune-"+mode))
		})
	}
}

func TestEnvtestSitePruneHierarchyManifestLifecycle(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	exerciseSitePruneManifestKinds(t, "complete", true, installWirelessFeatureQualificationAPI(t, api, "site-prune-hierarchy"), true)
}
