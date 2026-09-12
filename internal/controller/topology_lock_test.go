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
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

func TestDeviceTopologyLockAcquireRevalidateAndExactRelease(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	device := topologyLockDevice(target)
	ledger := policyFenceLedger(t, rollout, nil, nil, topologyrollout.ReservationReserved)
	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithObjects(device, ledger).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	now := time.Date(2026, 9, 12, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	lockID, err := r.acquireDeviceTopologyLock(ctx, rollout, target, 1, now)
	if err != nil {
		t.Fatalf("acquire topology lock: %v", err)
	}
	request := topologyrollout.ReservationRequest{
		ID:             reservationID(string(rollout.UID), target.DeviceUID),
		CampaignUID:    string(rollout.UID),
		PlanHash:       rollout.Status.FrozenPlan.Hash,
		PolicyUID:      rollout.Status.FrozenPlan.Policy.UID,
		PolicyVersion:  rollout.Status.FrozenPlan.Policy.ResourceVersion,
		PolicyEpoch:    1,
		TopologyLockID: lockID,
		PhysicalID:     target.PhysicalIdentity,
		DeviceUID:      target.DeviceUID,
		NodeUID:        target.NodeUID,
		ChildNamespace: rollout.Namespace,
		ChildName:      target.ChildName,
		Domains:        targetDomains(target),
	}
	if err := r.ledgerStore(rollout).Mutate(ctx, func(current *topologyrollout.Ledger) error {
		current.Reservations[request.ID] = topologyrollout.Reservation{
			ReservationRequest: request,
			State:              topologyrollout.ReservationReserved,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	recoveredID, err := r.acquireDeviceTopologyLock(ctx, rollout, target, 1, now.Add(time.Hour))
	if err != nil {
		t.Fatalf("idempotent acquire: %v", err)
	}
	if recoveredID != lockID {
		t.Fatalf("recovered acquisition ID = %q, want %q", recoveredID, lockID)
	}
	var current ciskov1.CiscoDevice
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.TopologyLock == nil || !current.Status.TopologyLock.AcquiredAt.Time.Equal(now) {
		t.Fatalf("topology lock=%#v, want stable acquisition time %s", current.Status.TopologyLock, now)
	}
	if err := r.revalidateDeviceTopologyLock(ctx, rollout, target, 1, lockID); err != nil {
		t.Fatalf("revalidate topology lock: %v", err)
	}
	if err := r.beginDeviceTopologyLockRelease(ctx, rollout, target, 1, lockID); err != nil {
		t.Fatalf("begin topology lock release: %v", err)
	}
	if err := r.ledgerStore(rollout).Mutate(ctx, func(current *topologyrollout.Ledger) error {
		return topologyrollout.ReleaseUnclaimedAtEpoch(current, request.ID, lockID, "", 0, 1)
	}); err != nil {
		t.Fatalf("release reservation: %v", err)
	}
	if err := r.releaseDeviceTopologyLock(ctx, rollout, target, 1, lockID); err != nil {
		t.Fatalf("release topology lock: %v", err)
	}
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.TopologyLock != nil {
		t.Fatalf("topology lock remains after exact ledger-absent release: %#v", current.Status.TopologyLock)
	}
}

func TestDeviceTopologyLockReleaseFenceBlocksOverlappingAcquireAndStaleCleanup(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	device := topologyLockDevice(target)
	oldLockID := strings.Repeat("1", 32)
	device.Status.TopologyLock = expectedDeviceTopologyLock(rollout, target, 1, oldLockID, time.Now().UTC())
	ledger := policyFenceLedger(t, rollout, nil, nil, topologyrollout.ReservationReserved)
	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithObjects(device, ledger).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	ctx := context.Background()

	// Stale releaser R reads the empty ledger and captures the old acquisition.
	staleEpoch, staleLockID, err := r.topologyLockAcquisitionForRelease(ctx, rollout, target, 1)
	if err != nil || staleEpoch != 1 || staleLockID != oldLockID {
		t.Fatalf("stale release snapshot = epoch %d id %q error %v", staleEpoch, staleLockID, err)
	}
	// Acquirer A is not allowed to reuse that published-but-unreserved identity.
	// It first wins Active->Releasing, commits an exact no-op ledger fence, and
	// clears the old lock. This is the ordering edge that makes R's stale read
	// harmless and also conflicts an old Reserve already in flight.
	if _, err := r.acquireDeviceTopologyLock(ctx, rollout, target, 1, time.Now().UTC()); err == nil ||
		!errors.Is(err, topologyrollout.ErrStaleControlRevision) {
		t.Fatalf("incomplete acquisition retirement error=%v, want stale-control retry", err)
	}
	newLockID, err := r.acquireDeviceTopologyLock(ctx, rollout, target, 1, time.Now().UTC())
	if err != nil {
		t.Fatalf("acquire after exact release: %v", err)
	}
	if newLockID == oldLockID {
		t.Fatal("new acquisition reused the released acquisition identity")
	}
	request := topologyrollout.ReservationRequest{
		ID:             reservationID(string(rollout.UID), target.DeviceUID),
		CampaignUID:    string(rollout.UID),
		PlanHash:       rollout.Status.FrozenPlan.Hash,
		PolicyUID:      rollout.Status.FrozenPlan.Policy.UID,
		PolicyVersion:  rollout.Status.FrozenPlan.Policy.ResourceVersion,
		PolicyEpoch:    1,
		TopologyLockID: newLockID,
		PhysicalID:     target.PhysicalIdentity,
		DeviceUID:      target.DeviceUID,
		NodeUID:        target.NodeUID,
		ChildNamespace: rollout.Namespace,
		ChildName:      target.ChildName,
		Domains:        targetDomains(target),
	}
	if err := r.ledgerStore(rollout).Mutate(ctx, func(current *topologyrollout.Ledger) error {
		current.Reservations[request.ID] = topologyrollout.Reservation{
			ReservationRequest: request, State: topologyrollout.ReservationReserved,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.revalidateDeviceTopologyLock(ctx, rollout, target, 1, newLockID); err != nil {
		t.Fatalf("post-reserve lock validation: %v", err)
	}

	// R resumes only after A has reserved and post-validated. Its old identity
	// cannot transition A's lock or delete A's reservation.
	if err := r.beginDeviceTopologyLockRelease(ctx, rollout, target, staleEpoch, staleLockID); err == nil {
		t.Fatal("stale cleanup accepted a newer acquisition")
	}
	if err := r.ledgerStore(rollout).Mutate(ctx, func(current *topologyrollout.Ledger) error {
		return topologyrollout.ReleaseUnclaimedAtEpoch(current, request.ID, staleLockID, "", 0, staleEpoch)
	}); !errors.Is(err, topologyrollout.ErrStaleControlRevision) {
		t.Fatalf("stale ledger release error=%v, want stale-control rejection", err)
	}
	var current ciskov1.CiscoDevice
	if err := apiClient.Get(ctx, client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.TopologyLock == nil || current.Status.TopologyLock.AcquisitionID != newLockID ||
		current.Status.TopologyLock.State != ciskov1.DeviceTopologyLockActive {
		t.Fatalf("stale cleanup changed newer lock: %#v", current.Status.TopologyLock)
	}
	_, currentLedger, err := r.ledgerStore(rollout).Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got := currentLedger.Reservations[request.ID].TopologyLockID; got != newLockID {
		t.Fatalf("stale cleanup changed newer reservation lock ID %q, want %q", got, newLockID)
	}
}

func TestDeviceTopologyLockReleaseConflictRechecksLedger(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	device := topologyLockDevice(target)
	lockID := strings.Repeat("1", 32)
	device.Status.TopologyLock = expectedDeviceTopologyLock(rollout, target, 1, lockID, time.Now().UTC())
	emptyLedger := policyFenceLedger(t, rollout, nil, nil, topologyrollout.ReservationReserved)
	populatedLedger := policyFenceLedger(t, rollout,
		[]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target}, nil, topologyrollout.ReservationReserved)
	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	conflictInjected := false
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithObjects(device, emptyLedger).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourcePatch: func(ctx context.Context, inner client.Client, subResource string,
				obj client.Object, patch client.Patch, opts ...client.SubResourcePatchOption) error {
				if subResource == "status" && !conflictInjected {
					conflictInjected = true
					var ledger corev1.ConfigMap
					if err := inner.Get(ctx, client.ObjectKeyFromObject(emptyLedger), &ledger); err != nil {
						return err
					}
					ledger.Data = populatedLedger.Data
					if err := inner.Update(ctx, &ledger); err != nil {
						return err
					}
					return apierrors.NewConflict(schema.GroupResource{Group: "cisco.vk", Resource: "ciscodevices"},
						obj.GetName(), errors.New("injected concurrent lock refresh"))
				}
				return inner.SubResource(subResource).Patch(ctx, obj, patch, opts...)
			},
		}).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	if err := r.beginDeviceTopologyLockRelease(context.Background(), rollout, target, 1, lockID); err != nil {
		t.Fatal(err)
	}
	err := r.releaseDeviceTopologyLock(context.Background(), rollout, target, 1, lockID)
	if err == nil || !strings.Contains(err.Error(), "reservation") {
		t.Fatalf("release after concurrent reservation error=%v, want reservation refusal", err)
	}
	if !conflictInjected {
		t.Fatal("test did not inject the status conflict")
	}
	var current ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.TopologyLock == nil {
		t.Fatal("retry cleared the topology lock after a concurrent reservation")
	}
}

func TestDeviceTopologyLockReleaseRequiresReservationAbsence(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	device := topologyLockDevice(target)
	lockID := strings.Repeat("1", 32)
	device.Status.TopologyLock = expectedDeviceTopologyLock(rollout, target, 1, lockID, time.Now().UTC())
	ledger := policyFenceLedger(t, rollout, []opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target}, nil, topologyrollout.ReservationReserved)
	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithObjects(device, ledger).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	if err := r.beginDeviceTopologyLockRelease(context.Background(), rollout, target, 1, lockID); err != nil {
		t.Fatal(err)
	}
	err := r.releaseDeviceTopologyLock(context.Background(), rollout, target, 1, lockID)
	if err == nil || !strings.Contains(err.Error(), "reservation") {
		t.Fatalf("release with reservation error=%v", err)
	}
	var current ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	if current.Status.TopologyLock == nil {
		t.Fatal("active reservation release cleared topology lock")
	}
}

func TestTopologyLockSettlementRecoversAfterLedgerRemovalAndLockClear(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	device := topologyLockDevice(target)
	leaf := policyFenceLeaf(rollout, target, "leaf-uid")
	lockID := leaf.Status.ManagerAdmission.TopologyLockID
	ledgerCM := policyFenceLedger(t, rollout, nil, nil, topologyrollout.ReservationReserved)
	ledger, err := topologyrollout.Decode([]byte(ledgerCM.Data[topologyrollout.LedgerDataKey]), string(ledgerCM.UID))
	if err != nil {
		t.Fatal(err)
	}
	// Another target may overwrite the single CAS token after this target's
	// reservation and device lock were already removed but before its leaf was
	// acknowledged Settled.
	if err := topologyrollout.FenceUnboundAcquisition(ledger, "other-reservation", 1, strings.Repeat("2", 32)); err != nil {
		t.Fatal(err)
	}
	encoded, err := topologyrollout.Encode(ledger, topologyrollout.DefaultMaxSerializedBytes)
	if err != nil {
		t.Fatal(err)
	}
	ledgerCM.Data[topologyrollout.LedgerDataKey] = string(encoded)
	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}, &opsv1alpha1.IOSXESoftwareUpgrade{}).
		WithObjects(device, leaf, ledgerCM).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	ctx := context.Background()

	// The retained manager admission is the durable per-reservation recovery
	// identity once both other transaction records are gone.
	epoch, recoveredID, err := r.topologyLockAcquisitionForRelease(ctx, rollout, target,
		leaf.Status.ManagerAdmission.PolicyEpoch, lockID)
	if err != nil || epoch != leaf.Status.ManagerAdmission.PolicyEpoch || recoveredID != lockID {
		t.Fatalf("recover leaf acquisition = epoch %d id %q error %v", epoch, recoveredID, err)
	}
	if err := r.beginDeviceTopologyLockRelease(ctx, rollout, target, epoch, recoveredID); err != nil {
		t.Fatal(err)
	}
	if err := r.ledgerStore(rollout).Mutate(ctx, func(current *topologyrollout.Ledger) error {
		return topologyrollout.Settle(current, leaf.Status.ManagerAdmission.ReservationID, recoveredID,
			string(leaf.UID), epoch, true, true)
	}); err != nil {
		t.Fatalf("replay absent-reservation settlement: %v", err)
	}
	if err := r.releaseDeviceTopologyLock(ctx, rollout, target, epoch, recoveredID); err != nil {
		t.Fatalf("replay cleared-lock settlement: %v", err)
	}
	_, current, err := r.ledgerStore(rollout).Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !topologyrollout.HasReleaseFence(current, leaf.Status.ManagerAdmission.ReservationID, epoch, recoveredID) {
		t.Fatalf("settlement replay did not restore exact release fence: %#v", current.LastReleaseFence)
	}
}

func TestDeviceTopologyLockRejectsForeignTransactionAndTargetDrift(t *testing.T) {
	target := policyFenceTarget("device-a", "device-uid", "leaf-a")
	rollout := policyFenceRollout([]opsv1alpha1.IOSXESoftwareRolloutPlannedTarget{target})
	device := topologyLockDevice(target)
	device.Status.TopologyLock = expectedDeviceTopologyLock(rollout, target, 1, strings.Repeat("1", 32), time.Now().UTC())
	device.Status.TopologyLock.CampaignUID = "foreign-campaign"
	scheme := newTestScheme(t)
	if err := opsv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	apiClient := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&ciskov1.CiscoDevice{}).
		WithObjects(device).Build()
	r := &IOSXESoftwareRolloutReconciler{Client: apiClient, APIReader: apiClient}
	if _, err := r.acquireDeviceTopologyLock(context.Background(), rollout, target, 1, time.Now().UTC()); err == nil ||
		!strings.Contains(err.Error(), "different active") {
		t.Fatalf("foreign lock acquire error=%v", err)
	}

	var current ciskov1.CiscoDevice
	if err := apiClient.Get(context.Background(), client.ObjectKeyFromObject(device), &current); err != nil {
		t.Fatal(err)
	}
	current.Status.TopologyLock = expectedDeviceTopologyLock(rollout, target, 1, strings.Repeat("1", 32), time.Now().UTC())
	current.Status.TopologyProjection.EffectiveLabelHash = "sha256:" + strings.Repeat("f", 64)
	if err := apiClient.Status().Update(context.Background(), &current); err != nil {
		t.Fatal(err)
	}
	if err := r.revalidateDeviceTopologyLock(context.Background(), rollout, target, 1, strings.Repeat("1", 32)); err == nil ||
		!strings.Contains(err.Error(), "projection changed") {
		t.Fatalf("projection drift error=%v", err)
	}
}

func topologyLockDevice(target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) *ciskov1.CiscoDevice {
	return &ciskov1.CiscoDevice{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "lab", Name: target.DeviceName, UID: types.UID(target.DeviceUID), Generation: target.DeviceGeneration,
		},
		Spec: ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE, PhysicalIdentity: target.PhysicalIdentity},
		Status: ciskov1.DeviceStatus{
			NodeIdentity: &ciskov1.DeviceNodeIdentityStatus{
				NodeName: target.NodeName, NodeUID: target.NodeUID, DeviceUID: target.DeviceUID,
				PhysicalIdentity: target.PhysicalIdentity,
			},
			TopologyProjection: &ciskov1.DeviceTopologyProjectionStatus{
				EffectiveLabelHash: target.ProjectionHash, LastSuccessfulTime: metav1.Now(),
			},
		},
	}
}
