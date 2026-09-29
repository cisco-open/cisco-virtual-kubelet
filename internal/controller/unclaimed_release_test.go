// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package controller

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

func unclaimedReleaseFixture(
	t *testing.T,
	withClaim bool,
) (*IOSXESoftwareRolloutReconciler, *opsv1alpha1.IOSXESoftwareRollout, opsv1alpha1.IOSXESoftwareRolloutPlannedTarget, *opsv1alpha1.IOSXESoftwareUpgrade, string) {
	t.Helper()
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	lockID := strings.Repeat("1", 32)
	device := topologyLockDevice(target)
	device.Status.TopologyLock = expectedDeviceTopologyLock(rollout, target, 1, lockID, metav1.Now().Time)
	leaf := policyFenceLeaf(rollout, target, types.UID("leaf-uid"))
	leaf.Status.ManagerAdmission.State = opsv1alpha1.UpgradeManagerAdmissionRevoked
	leaf.Status.ManagerAdmission.RevocationReason = "CampaignCancelled"
	if withClaim {
		leaf.Status.ManagedMutationClaims = []opsv1alpha1.UpgradeManagedMutationClaimStatus{{
			Stage: opsv1alpha1.UpgradeManagedMutationPrimaryInstall, PolicyEpoch: 1,
		}}
	}
	ledger := policyFenceLedger(t, rollout, []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target},
		map[string]types.UID{target.DeviceUID: leaf.UID}, topologyrollout.ReservationGranted)
	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(device, leaf, ledger).Build()
	return &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}, rollout, target, leaf, lockID
}

func TestReleaseUnclaimedReservationRevokesExactZeroClaimGrantFirst(t *testing.T) {
	r, rollout, target, leaf, lockID := unclaimedReleaseFixture(t, false)
	ctx := context.Background()
	if err := r.releaseUnclaimedReservationAtEpoch(ctx, rollout, target, string(leaf.UID),
		uint64(rollout.Spec.Control.Revision), 1, lockID); err != nil {
		t.Fatalf("release exact unclaimed grant: %v", err)
	}
	_, ledger, err := r.ledgerStore(rollout).Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := ledger.Reservations[reservationID(string(rollout.UID), target.DeviceUID)]; exists {
		t.Fatal("exactly revoked reservation remains after release")
	}
	var device ciskov1.CiscoDevice
	if err := r.Get(ctx, client.ObjectKey{Namespace: rollout.Namespace, Name: target.DeviceName}, &device); err != nil {
		t.Fatal(err)
	}
	if device.Status.TopologyLock != nil {
		t.Fatalf("topology lock remains after exact release: %#v", device.Status.TopologyLock)
	}
}

func TestReleaseUnclaimedReservationRejectsClaimRacingStaleZeroClaimRead(t *testing.T) {
	r, rollout, target, leaf, lockID := unclaimedReleaseFixture(t, true)
	// Model a manager that observed zero claims before the worker's claim-status
	// CAS. releaseUnclaimedReservationAtEpoch must ignore that stale observation
	// and perform its own uncached read inside the ledger mutation attempt.
	staleLeaf := leaf.DeepCopy()
	staleLeaf.Status.ManagedMutationClaims = nil
	if len(staleLeaf.Status.ManagedMutationClaims) != 0 {
		t.Fatal("stale fixture unexpectedly contains a mutation claim")
	}

	err := r.releaseUnclaimedReservationAtEpoch(context.Background(), rollout, target, string(leaf.UID),
		uint64(rollout.Spec.Control.Revision), 1, lockID)
	if err == nil || !strings.Contains(err.Error(), "without durable mutation claims") {
		t.Fatalf("racing durable claim release error = %v", err)
	}
	_, ledger, readErr := r.ledgerStore(rollout).Read(context.Background())
	if readErr != nil {
		t.Fatal(readErr)
	}
	reservation := ledger.Reservations[reservationID(string(rollout.UID), target.DeviceUID)]
	if reservation.State != topologyrollout.ReservationGranted {
		t.Fatalf("racing claim changed granted reservation to %s", reservation.State)
	}
}

func TestReleaseUnclaimedReservationRecoversAfterDurableLedgerRevoke(t *testing.T) {
	r, rollout, target, leaf, lockID := unclaimedReleaseFixture(t, false)
	ctx := context.Background()
	store := r.ledgerStore(rollout)
	if err := r.revokeExactUnclaimedReservation(ctx, store, rollout, target, string(leaf.UID),
		uint64(rollout.Spec.Control.Revision), 1, lockID); err != nil {
		t.Fatalf("persist exact unclaimed revocation: %v", err)
	}
	_, ledger, err := store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := ledger.Reservations[reservationID(string(rollout.UID), target.DeviceUID)].State; got != topologyrollout.ReservationRevoked {
		t.Fatalf("crash boundary reservation state = %s, want Revoked", got)
	}

	// A fresh reconciler uses the durable Revoked state as prior zero-claim
	// evidence and can finish the exact release after a manager restart.
	restarted := &IOSXESoftwareRolloutReconciler{Client: r.Client, APIReader: r.APIReader}
	if err := restarted.releaseUnclaimedReservationAtEpoch(ctx, rollout, target, string(leaf.UID),
		uint64(rollout.Spec.Control.Revision), 1, lockID); err != nil {
		t.Fatalf("restart release of revoked reservation: %v", err)
	}
	_, ledger, err = store.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := ledger.Reservations[reservationID(string(rollout.UID), target.DeviceUID)]; exists {
		t.Fatal("restart left the durably revoked reservation present")
	}
}
