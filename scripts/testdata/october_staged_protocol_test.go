// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

// This fixture is compiled in the pinned October release, not in current HEAD.
package softwareupgrade

import (
	"context"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestOctoberWorkerStagedProtocolFence(t *testing.T) {
	for _, protocol := range []string{"rollout-v1", "rollout-staged-activation-v1"} {
		t.Run(protocol, func(t *testing.T) {
			up := managedTestLeaf("compat-prepare")
			// The October type accepts this string from a newer served schema,
			// although its execution code does not implement PrepareOnly.
			up.Spec.Strategy = ops.UpgradeStrategy("PrepareOnly")
			up.Status.ManagerAdmission.ProtocolVersion = ops.ManagedUpgradeProtocolVersion(protocol)
			r := newManagedTestReconciler(t, up, nil)
			decision := r.evaluateManagedLeaf(context.Background(), up)
			if protocol == "rollout-v1" {
				if !decision.allowProgress || !decision.allowClaim {
					t.Fatalf("positive control did not reproduce legacy acceptance: %+v", decision)
				}
			} else if decision.allowProgress || decision.allowClaim {
				t.Fatalf("released worker accepted new staged protocol: %+v", decision)
			}
		})
	}
}
