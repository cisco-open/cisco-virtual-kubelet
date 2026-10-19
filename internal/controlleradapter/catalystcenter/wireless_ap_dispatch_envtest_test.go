//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestEnvtestWirelessAPDispatchRecovery(t *testing.T) {
	api := qualificationAPIServerClient(t, "../../../config/crd/config.cisco.vk_networkcontrollerconfigs.yaml")
	for _, mode := range []string{"field-edit", "read-error", "lost-ack"} {
		t.Run("dispatch-"+mode, func(t *testing.T) {
			exerciseWirelessNoDispatchReplan(t, wirelessAPDispatchCase, mode, installWirelessFeatureQualificationAPI(t, api, "ap-dispatch-"+mode))
		})
	}
	for _, mode := range []string{"known", "unknown-create", "child-failed", "tree-unavailable", "changed-source", "replaced-target", "create-child-pending", "create-taskless"} {
		t.Run("recovery-"+mode, func(t *testing.T) {
			exerciseWirelessDefinitionRecoveryPublic(t, wirelessAPDispatchCase, mode, installWirelessFeatureQualificationAPI(t, api, "ap-recovery-"+mode))
		})
	}
}
