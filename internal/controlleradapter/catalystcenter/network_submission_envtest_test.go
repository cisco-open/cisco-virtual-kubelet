// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

// Actual API owner/journal/source persistence; native responses are fixtures.
func TestEnvtestNetworkSubmissionRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
		for _, mode := range []string{"complete", "read-error", "stale", "lost-ack"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				exerciseNetworkSubmissionRecovery(t, kind, mode, installWirelessFeatureQualificationAPI(t, api, "network-submit-"+kind+"-"+mode))
			})
		}
	}
}
