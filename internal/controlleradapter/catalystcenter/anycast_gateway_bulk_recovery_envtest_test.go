//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestAnycastBulkAcknowledgedRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"public-accepted-child-failed", "public-accepted-child-pending", "public-unknown", "public-partial", "public-option-removed", "public-child-failed", "public-child-pending", "public-tree-unavailable", "public-source-changed", "public-parent-replaced", "public-taskless", "public-binding-changed", "public-existing-replaced", "public-legacy-known"} {
		t.Run(mode, func(t *testing.T) {
			exerciseAnycastBulkChunks(t, mode, installWirelessFeatureQualificationAPI(t, api, "anycast-"+mode))
		})
	}
}
