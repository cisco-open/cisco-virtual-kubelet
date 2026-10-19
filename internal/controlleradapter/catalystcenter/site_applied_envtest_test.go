// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestSitePrivateApplied(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, stage := range []string{"building-create", "floor-create"} {
		for _, mode := range siteAppliedModes {
			t.Run(stage+"/"+mode, func(t *testing.T) {
				exerciseSiteAppliedPublic(t, stage, mode, installWirelessFeatureQualificationAPI(t, api, "site-applied-"+stage+"-"+mode))
			})
		}
	}
}
