// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwareupgrade

import (
	"context"
	"fmt"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestPrepareOnlyRequiresStagedProtocolBeforeProgress(t *testing.T) {
	for _, rate := range []int64{0, 25_000_000} {
		for _, protocol := range []ops.ManagedUpgradeProtocolVersion{
			ops.ManagedUpgradeProtocolRolloutV1,
			ops.ManagedUpgradeProtocolRolloutBytePacingV1,
			ops.ManagedUpgradeProtocolStagedActivationV1,
		} {
			t.Run(fmt.Sprintf("%s/%d", protocol, rate), func(t *testing.T) {
				up := managedTestLeaf("prepare-protocol")
				up.Spec.Strategy = ops.UpgradeStrategyPrepareOnly
				up.Spec.MaxTransferBytesPerSecond = rate
				up.Status.ManagerAdmission.ProtocolVersion = protocol
				r := newManagedTestReconciler(t, up, nil)
				decision := r.evaluateManagedLeaf(context.Background(), up)
				want := protocol == ops.ManagedUpgradeProtocolStagedActivationV1
				if decision.allowClaim != want || decision.allowProgress != want {
					t.Fatalf("rate=%d protocol=%s: %+v, want progress/claim=%t", rate, protocol, decision, want)
				}
			})
		}
	}
}
