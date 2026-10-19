//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestSiteDefaultsManifestLifecycle(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	exerciseSitePruneManifestKinds(t, "complete", true, installWirelessFeatureQualificationAPI(t, api, "site-defaults-hierarchy"), true, func(raw []byte) []byte { return siteDefaultResolvedFixture(t, raw) })
}
