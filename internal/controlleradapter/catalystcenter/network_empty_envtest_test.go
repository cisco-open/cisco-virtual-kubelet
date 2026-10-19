// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import "testing"

func TestEnvtestNetworkEmpty(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, kind := range []string{"ntp", "dhcp"} {
		for _, mode := range []string{"complete", "parent-nonempty", "inherited", "preflight-race", "lost-ack", "native-race", "malformed-read", "already-clear"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				exerciseNetworkEmpty(t, kind, mode, installWirelessFeatureQualificationAPI(t, api, "empty-"+kind+"-"+mode))
			})
		}
	}
}
