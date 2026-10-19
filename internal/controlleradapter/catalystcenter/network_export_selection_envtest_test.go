//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestNetworkExportSelection(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
		for _, mode := range []string{"reselect", "accepted-null", "unknown-null", "verified-null", "corrupt-history", "missing-site"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				exerciseNetworkSelection(t, kind, "scoped-"+mode, installWirelessFeatureQualificationAPI(t, api, "scope-"+kind+"-"+mode))
			})
		}
	}
}
