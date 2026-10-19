// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestNetworkSelection(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner"} {
		for _, mode := range []string{"omitted", "null", "dns-unselected", "missing-site", "reselect", "accepted-null", "unknown-null", "verified-null", "corrupt-history"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				exerciseNetworkSelection(t, kind, mode, installWirelessFeatureQualificationAPI(t, api, "selection-"+kind+"-"+mode))
			})
		}
	}
}
