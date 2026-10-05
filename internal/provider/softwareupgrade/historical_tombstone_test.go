// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwareupgrade

import (
	"reflect"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestHistoricalEmptyPreparationTombstoneRemainsAuditOnly(t *testing.T) {
	for _, scenario := range []string{"empty-cancelled", "claimed", "worker-control", "receipt", "marker", "condition", "message", "future-protocol", "network", "preinstalled", "not-cancelled", "paused", "granted", "identity", "control", "nonempty-phase"} {
		t.Run(scenario, func(t *testing.T) {
			up := managedCancellationQueueTombstone("historical-prepare")
			up.Spec.Strategy = ops.UpgradeStrategyPrepareOnly
			switch scenario {
			case "claimed":
				up.Status.ManagedMutationClaims = []ops.UpgradeManagedMutationClaimStatus{{Stage: ops.UpgradeManagedMutationPrimaryInstall}}
			case "worker-control":
				up.Status.WorkerControl = &ops.UpgradeWorkerControlStatus{}
			case "receipt":
				up.Status.PreparedReceipt = &ops.UpgradePreparedReceiptStatus{}
			case "marker":
				up.Status.PrimarySupervisorInstallRequested = true
			case "condition":
				up.Status.Conditions = []metav1.Condition{{Type: "Started", Status: metav1.ConditionUnknown}}
			case "message":
				up.Status.Message = "worker was here"
			case "future-protocol":
				up.Status.ManagerAdmission.ProtocolVersion = "future"
			case "network":
				up.Spec.RequireNetworkEvidence = true
			case "preinstalled":
				up.Spec.ImageSource.Preinstalled = &ops.PreinstalledImageSource{}
			case "not-cancelled":
				up.Status.ManagerControl.Cancel = false
			case "paused":
				up.Status.ManagerControl.Pause = true
			case "granted":
				up.Status.ManagerAdmission.State = ops.UpgradeManagerAdmissionGranted
			case "identity":
				up.Status.ManagerAdmission.LeafUID = "other"
			case "control":
				up.Status.ManagerControl.Revision++
			case "nonempty-phase":
				up.Status.Phase = ops.UpgradePhaseTransferring
			}
			before := up.DeepCopy()
			if got := SettledUnclaimedManagedOperation(up); got != (scenario == "empty-cancelled") {
				t.Fatalf("settled unclaimed=%v for %s", got, scenario)
			}
			if ops.ManagedUpgradeProtocolMatches(up) {
				t.Fatal("historical tombstone changed live protocol eligibility")
			}
			if !reflect.DeepEqual(before, up) {
				t.Fatal("historical audit was rewritten")
			}
		})
	}
}
