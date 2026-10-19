//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestSiteRetirementReceipts(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"complete", "latest", "unknown", "generic", "pending", "failed", "conflict", "lost-response", "pruned-receipt", "pruned-creator", "pruned-operation", "capacity", "bytes", "edit-target", "edit-prepared", "edit-source", "edit-row-target", "edit-result", "edit-both-results", "missing-time", "future-time"} {
		t.Run(mode, func(t *testing.T) {
			exerciseSiteRetirementBinding(t, "recovery-receipt-"+mode, true, installWirelessFeatureQualificationAPI(t, api, "retire-receipt-"+mode))
		})
	}
}
