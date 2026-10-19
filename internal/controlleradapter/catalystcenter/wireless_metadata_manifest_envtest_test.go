//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestWirelessMetadataOnlyManifests(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	t.Run("ap-calendar", func(t *testing.T) {
		exerciseWirelessAPCalendarManifestWithoutCredentials(t, installWirelessFeatureQualificationAPI(t, api, "metadata-ap-calendar"))
	})
	t.Run("open-ssid", func(t *testing.T) {
		exerciseWirelessOpenSSIDManifestWithoutCredentials(t, installWirelessFeatureQualificationAPI(t, api, "metadata-open-ssid"))
	})
}
