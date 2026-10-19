//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestNetworkNativeComparison(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "companion", "restore-companion", "key", "restore-key", "missing-key", "proof", "downgrade", "lost-ack", "noop", "drift", "drop-key", "legacy", "replace", "retarget", "missing-state", "unapproved-key"} {
		t.Run(mode, func(t *testing.T) {
			exerciseNetworkNativeComparison(t, mode, installWirelessFeatureQualificationAPI(t, api, "native-network-"+mode))
		})
	}
}
