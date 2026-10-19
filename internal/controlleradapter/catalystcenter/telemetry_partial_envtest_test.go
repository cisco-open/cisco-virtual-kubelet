//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestTelemetryPartial(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"wired", "syslog", "snmp", "enable", "builtin", "null", "noop", "all-null", "missing", "race", "lost-ack", "source-null", "unknown-native"} {
		t.Run(mode, func(t *testing.T) {
			exerciseTelemetryPartial(t, mode, installWirelessFeatureQualificationAPI(t, api, "telemetry-partial-"+mode))
		})
	}
}
