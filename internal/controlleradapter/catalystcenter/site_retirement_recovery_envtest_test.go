//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestSiteRetirementTaskRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"accepted", "unknown", "verifying", "latest", "latest-pruned", "edited-state-proof", "edited-ancestry-proof", "replacement", "parent-replaced", "reappear", "failed-child", "pending-child", "changed-completion", "final-failure", "task-source", "task-owner", "task-key", "absence-history", "absence-phase"} {
		t.Run(mode, func(t *testing.T) {
			exerciseSiteRetirementBinding(t, "recovery-"+mode, true, installWirelessFeatureQualificationAPI(t, api, "retire-recovery-"+mode))
		})
	}
}
