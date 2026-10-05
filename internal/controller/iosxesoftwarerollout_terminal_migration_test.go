// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestTerminalDrainLegacyNetworkProtocolIsCleanupOnly(t *testing.T) {
	for _, scenario := range []string{"settled", "cancelled", "active-campaign", "granted", "unsettled-drain", "unresolved-claim", "wrong-source", "wrong-target", "wrong-device", "wrong-protocol", "wrong-control", "wrong-epoch", "staged"} {
		t.Run(scenario, func(t *testing.T) {
			_, rollout, leaf, _ := settledDrainAcknowledgementFixture(t)
			target := rollout.Status.FrozenPlan.Targets[0]
			rollout.Status.Phase = ops.IOSXESoftwareRolloutPhaseSucceeded
			rollout.Spec.Plan.Health.Network = &ops.IOSXESoftwareRolloutNetworkHealthSpec{Enabled: true, RequireCompleteEvidence: true}
			now := metav1.NewTime(time.Now().UTC())
			leaf.Status.Phase = ops.UpgradePhaseSucceeded
			leaf.Status.CompletionTime = &now
			leaf.Status.ManagedMutationClaims = []ops.UpgradeManagedMutationClaimStatus{{Stage: ops.UpgradeManagedMutationPrimaryInstall, PolicyEpoch: leaf.Status.ManagerAdmission.PolicyEpoch}}
			leaf.Status.Conditions = []metav1.Condition{{Type: "DeviceMutationSettled", Status: metav1.ConditionTrue}}
			switch scenario {
			case "cancelled":
				rollout.Status.Phase = ops.IOSXESoftwareRolloutPhaseCancelled
			case "active-campaign":
				rollout.Status.Phase = ops.IOSXESoftwareRolloutPhaseExecuting
			case "granted":
				leaf.Status.ManagerAdmission.State = ops.UpgradeManagerAdmissionGranted
			case "unsettled-drain":
				leaf.Status.ManagerDrain.State = ops.UpgradeManagerDrainRecovering
			case "unresolved-claim":
				leaf.Status.Conditions = nil
			case "wrong-source":
				leaf.Spec.ImageSource.URL = "https://other.example/image.bin"
			case "wrong-target":
				leaf.Spec.TargetVersion = "17.18.99"
			case "wrong-device":
				leaf.Status.ManagerAdmission.DeviceUID = "other"
			case "wrong-protocol":
				leaf.Status.ManagerAdmission.ProtocolVersion = "unknown"
			case "wrong-control":
				leaf.Status.ManagerControl.Revision++
			case "wrong-epoch":
				leaf.Status.ManagedMutationClaims[0].PolicyEpoch++
			case "staged":
				leaf.Spec.Strategy = ops.UpgradeStrategyPrepareOnly
			}
			beforeRollout, beforeLeaf := rollout.DeepCopy(), leaf.DeepCopy()
			wantPass := scenario == "settled" || scenario == "cancelled"
			if err := validateTerminalManagerDrainBinding(rollout, target, leaf); (err == nil) != wantPass {
				t.Fatalf("terminal compatibility: %v, want pass=%v", err, wantPass)
			}
			if err := validateManagerDrainBinding(rollout, target, leaf); err == nil {
				t.Fatal("live drain validation accepted old network protocol")
			}
			if !reflect.DeepEqual(rollout, beforeRollout) || !reflect.DeepEqual(leaf, beforeLeaf) {
				t.Fatal("compatibility validation rewrote retained audit")
			}
		})
	}
}
