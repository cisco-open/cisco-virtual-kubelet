// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

// Compiled against the pinned released manager, not the current implementation.
package controller

import (
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestOctoberManagerRejectsNewWorkerProtocols(t *testing.T) {
	for _, protocol := range []string{"rollout-v1", "rollout-byte-pacing-v1", "rollout-staged-activation-v1", "rollout-network-evidence-v1"} {
		for _, state := range []ops.UpgradeManagerAdmissionState{ops.UpgradeManagerAdmissionGranted, ops.UpgradeManagerAdmissionSettled} {
			t.Run(protocol+"/"+string(state), func(t *testing.T) {
				target := policyFenceTarget("edge", "device-uid", "leaf")
				rollout := policyFenceRollout([]ops.IOSXESoftwareRolloutPlannedTarget{target})
				leaf := policyFenceLeaf(rollout, target, "leaf-uid")
				leaf.Status.ManagerAdmission.ProtocolVersion = ops.ManagedUpgradeProtocolVersion(protocol)
				leaf.Status.ManagerAdmission.State = state
				err := validateManagerAdmission(rollout, target, leaf)
				if (err == nil) != (protocol == "rollout-v1") {
					t.Fatalf("released manager protocol=%s state=%s: %v", protocol, state, err)
				}
			})
		}
	}
}
