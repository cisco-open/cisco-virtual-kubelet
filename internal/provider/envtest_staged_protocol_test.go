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
	for _, name := range []string{"prepare", "activation", "network"} {
		up := newUpgrade(name, namespace, "17.18.03")
		if name == "prepare" {
			up.Spec.Strategy = ops.UpgradeStrategyPrepareOnly
		} else if name == "activation" {
			up.Spec.ImageSource = ops.UpgradeImageSource{Preinstalled: &ops.PreinstalledImageSource{}}
		} else {
			up.Spec.RequireNetworkEvidence = true
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
		denial := "staged lifecycle protocol"
		if name == "network" {
			denial = "network-gated grants require"
			up.Status.ManagerAdmission.ProtocolVersion = ops.ManagedUpgradeProtocolNetworkEvidenceV1
			if err := c.Status().Update(ctx, up); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), denial) {
				t.Fatalf("missing network evidence was not rejected: %v", err)
			}
			up.Status.ManagerAdmission.NetworkEvidenceHash = "sha256:" + strings.Repeat("b", 64)
			up.Status.ManagerAdmission.NetworkEvidenceProducerRevision = "revision"
			up.Status.ManagerAdmission.NetworkEvidenceWorkerPodUID = "worker-pod"
			up.Status.ManagerAdmission.NetworkEvidenceSampleSequence = ptr.To[uint64](1)
			up.Status.ManagerAdmission.NetworkEvidenceNotAfter = ptr.To(metav1.NewTime(time.Now().UTC().Add(time.Minute)))
		}
		for _, legacy := range []ops.ManagedUpgradeProtocolVersion{ops.ManagedUpgradeProtocolRolloutV1, ops.ManagedUpgradeProtocolRolloutBytePacingV1} {
			up.Status.ManagerAdmission.ProtocolVersion = legacy
			err := c.Status().Update(ctx, up)
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), denial) {
				t.Fatalf("%s legacy protocol %s: got %v, want exact protocol denial", name, legacy, err)
			}
		}
		up.Status.ManagerAdmission.ProtocolVersion = ops.RequiredManagedUpgradeProtocol(up.Spec)
		if err := c.Status().Update(ctx, up); err != nil {
			t.Fatalf("%s staged positive control rejected: %v", name, err)
		}
		if name == "network" {
			// An older typed manager would omit this unknown field on Update.
			// The immutable spec must prevent that from erasing the requirement.
			up.Spec.RequireNetworkEvidence = false
			if err := c.Update(ctx, up); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "spec is immutable") {
				t.Fatalf("network gate removal was not rejected: %v", err)
			}
		}
	}
}
