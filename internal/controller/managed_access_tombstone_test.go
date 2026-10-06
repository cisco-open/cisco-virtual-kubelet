// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package controller

import (
	"context"
	"reflect"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestAccessTransitionPreservesHistoricalEmptyPreparation(t *testing.T) {
	for _, claimed := range []bool{false, true} {
		t.Run(map[bool]string{false: "empty-settled", true: "claimed-blocked"}[claimed], func(t *testing.T) {
			ctx := context.Background()
			fixture := newLegacyHandoffFixture(t)
			device := fixture.device(t)
			target := policyFenceTarget(device.Name, string(device.UID), "old-preparation")
			target.NodeName, target.NodeUID = device.Status.NodeIdentity.NodeName, device.Status.NodeIdentity.NodeUID
			rollout := policyFenceRollout([]ops.IOSXESoftwareRolloutPlannedTarget{target})
			rollout.Namespace = device.Namespace
			leaf := policyFenceLeaf(rollout, target, "historical-leaf-uid")
			leaf.Spec.Strategy = ops.UpgradeStrategyPrepareOnly
			leaf.Status.ManagerAdmission.State = ops.UpgradeManagerAdmissionSettled
			leaf.Status.ManagerControl.Cancel = true
			leaf.Status.ManagerControl.Pause = false
			leaf.Annotations[managedprotocol.AnnotationNodeUID] = target.NodeUID
			if claimed {
				leaf.Status.ManagedMutationClaims = []ops.UpgradeManagedMutationClaimStatus{{
					Stage: ops.UpgradeManagedMutationPrimaryInstall, ClaimedAt: metav1.NewTime(fixture.clock.Now()),
				}}
			}
			if err := fixture.client.Create(ctx, leaf); err != nil {
				t.Fatal(err)
			}
			var before, after ops.IOSXESoftwareUpgrade
			if err := fixture.client.Get(ctx, client.ObjectKeyFromObject(leaf), &before); err != nil {
				t.Fatal(err)
			}
			err := fixture.r.ensureManagedDeviceAuthoritiesSettledForAccessTransition(ctx, device)
			if (err != nil) != claimed {
				t.Fatalf("claimed=%v migration error=%v", claimed, err)
			}
			if err := fixture.client.Get(ctx, client.ObjectKeyFromObject(leaf), &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatal("access transition changed historical audit")
			}
		})
	}
}
