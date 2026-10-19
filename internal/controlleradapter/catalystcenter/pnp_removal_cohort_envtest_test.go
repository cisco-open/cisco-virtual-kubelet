//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestPnPRemovalCohort(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range pnpRemovalCohortModes {
		t.Run(mode, func(t *testing.T) {
			exercisePnPRemovalCohort(t, mode, installWirelessFeatureQualificationAPI(t, api, "pnp-cohort-"+mode))
		})
	}
}
