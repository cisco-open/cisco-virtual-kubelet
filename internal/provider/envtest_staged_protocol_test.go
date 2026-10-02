// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

//go:build envtest

package provider

import (
	"context"
	"strings"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

func TestEnvtest_StagedLifecycleRejectsLegacyManagerGrant(t *testing.T) {
	c, stop := startEnvtest(t)
	defer stop()
	const namespace = "envtest-staged-protocol"
	envtestNamespace(t, c, namespace)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, prepare := range []bool{true, false} {
		name := "activation"
		if prepare {
			name = "prepare"
		}
		up := newUpgrade(name, namespace, "17.18.03")
		if prepare {
			up.Spec.Strategy = ops.UpgradeStrategyPrepareOnly
		} else {
			up.Spec.ImageSource = ops.UpgradeImageSource{Preinstalled: &ops.PreinstalledImageSource{}}
		}
		if err := c.Create(ctx, up); err != nil {
			t.Fatal(err)
		}
		up.Status.ManagerAdmission = &ops.UpgradeManagerAdmissionStatus{
			ProtocolVersion: ops.ManagedUpgradeProtocolRolloutV1,
			State:           ops.UpgradeManagerAdmissionGranted, PolicyEpoch: 1,
			TopologyLockID: strings.Repeat("1", 32), DeviceGeneration: 1,
			CampaignUID: "campaign", PlanHash: "sha256:" + strings.Repeat("a", 64),
			PolicyUID: "policy", PolicyResourceVersion: "1", LedgerUID: "ledger",
			ReservationID: "reservation", LeafUID: string(up.UID), DeviceUID: "device",
			PhysicalIdentity: "serial", NodeUID: "node", ControlRevision: ptr.To[int64](0),
			UpdatedAt: metav1.NewTime(time.Now().UTC()),
		}
		for _, legacy := range []ops.ManagedUpgradeProtocolVersion{ops.ManagedUpgradeProtocolRolloutV1, ops.ManagedUpgradeProtocolRolloutBytePacingV1} {
			up.Status.ManagerAdmission.ProtocolVersion = legacy
			err := c.Status().Update(ctx, up)
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "staged lifecycle protocol") {
				t.Fatalf("%s legacy protocol %s: got %v, want exact protocol denial", name, legacy, err)
			}
		}
		up.Status.ManagerAdmission.ProtocolVersion = ops.ManagedUpgradeProtocolStagedActivationV1
		if err := c.Status().Update(ctx, up); err != nil {
			t.Fatalf("%s staged positive control rejected: %v", name, err)
		}
	}
}
