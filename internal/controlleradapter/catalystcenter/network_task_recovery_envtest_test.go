// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestNetworkAcknowledgedTaskRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
		for _, mode := range []string{"complete", "root-running", "child-running", "child-failed", "disconnected", "missing-task", "wrong-target", "propagation", "native-race", "source-race", "owner-race", "journal-race", "configmap", "configmap-race"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				exerciseNetworkTaskRecovery(t, kind, mode, installWirelessFeatureQualificationAPI(t, api, "network-task-"+kind+"-"+mode))
			})
		}
	}
}
