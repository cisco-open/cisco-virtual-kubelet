// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

// Native task/readback and faulting writers are controlled fixtures. Controller,
// config, generation, source and application storage use an actual private API.
func TestEnvtestNetworkApplications(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
		for _, mode := range []string{"complete", "unknown", "replace", "target-replaced", "duplicate", "logical-duplicate", "legacy", "generic-verified", "drop-receipt", "drop-seal", "drop-companion", "lost-ack", "history-race", "preflight-history", "preflight-history-read-failure", "replace-full", "drop-row-prefix"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				exerciseNetworkApplication(t, kind, mode, installWirelessFeatureQualificationAPI(t, api, "network-applied-"+kind+"-"+mode))
			})
		}
	}
}

func TestEnvtestNetworkDNSOptional(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"omitted", "null", "clear", "inherited", "match"} {
		t.Run(mode, func(t *testing.T) {
			exerciseDNSOptional(t, mode, installWirelessFeatureQualificationAPI(t, api, "dns-optional-"+mode))
		})
	}
}
