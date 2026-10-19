//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestSiteRetirementDispatch(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range siteRetirementDispatchModes {
		t.Run(mode, func(t *testing.T) {
			exerciseSiteRetirementBinding(t, "write-"+mode, true, installWirelessFeatureQualificationAPI(t, api, "retire-dispatch-"+mode))
		})
	}
}
