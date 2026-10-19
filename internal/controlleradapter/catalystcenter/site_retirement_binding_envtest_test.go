//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestSiteRetirementBinding(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"stable", "latest", "declared", "dispatch-source", "dispatch-owner", "dispatch-history", "dispatch-key", "dispatch-target", "dispatch-task", "dispatch-phase", "dispatch-caller-memory", "dispatch-late-owner", "dispatch-late-native", "dispatch-late-phase"} {
		t.Run(mode, func(t *testing.T) {
			exerciseSiteRetirementBinding(t, mode, true, installWirelessFeatureQualificationAPI(t, api, "site-retire-"+mode))
		})
	}
}
