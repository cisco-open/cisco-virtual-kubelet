//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestPnPRemovalBaseline(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range pnpBaselineModes {
		t.Run(mode, func(t *testing.T) {
			exercisePnPRemovalBaseline(t, mode, installWirelessFeatureQualificationAPI(t, api, "pnp-baseline-"+mode))
		})
	}
}
