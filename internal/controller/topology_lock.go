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
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"reflect"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyrollout"
)

func expectedDeviceTopologyLock(
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	policyEpoch int64,
	acquisitionID string,
	acquiredAt time.Time,
) *ciskov1.DeviceTopologyLockStatus {
	return &ciskov1.DeviceTopologyLockStatus{
		State:             ciskov1.DeviceTopologyLockActive,
		PolicyEpoch:       policyEpoch,
		AcquisitionID:     acquisitionID,
		CampaignNamespace: rollout.Namespace,
		CampaignName:      rollout.Name,
		CampaignUID:       string(rollout.UID),
		PlanHash:          rollout.Status.FrozenPlan.Hash,
		ReservationID:     reservationID(string(rollout.UID), target.DeviceUID),
		DeviceUID:         target.DeviceUID,
		DeviceGeneration:  target.DeviceGeneration,
		NodeUID:           target.NodeUID,
		ProjectionHash:    target.ProjectionHash,
		AcquiredAt:        metav1.NewTime(acquiredAt.UTC()),
	}
}

func topologyLockIdentityEqual(actual, expected *ciskov1.DeviceTopologyLockStatus) bool {
	if actual == nil || expected == nil || actual.AcquiredAt.IsZero() {
		return false
	}
	return actual.State == expected.State && topologyLockAcquisitionEqual(actual, expected)
}

func topologyLockAcquisitionEqual(actual, expected *ciskov1.DeviceTopologyLockStatus) bool {
	if actual == nil || expected == nil || actual.AcquiredAt.IsZero() {
		return false
	}
	left := actual.DeepCopy()
	right := expected.DeepCopy()
	left.State = ""
	right.State = ""
	left.AcquiredAt = metav1.Time{}
	right.AcquiredAt = metav1.Time{}
	return reflect.DeepEqual(left, right)
}

func topologyLockCoordinatesEqual(actual, expected *ciskov1.DeviceTopologyLockStatus) bool {
	if actual == nil || expected == nil || actual.AcquiredAt.IsZero() {
		return false
	}
	left := actual.DeepCopy()
	right := expected.DeepCopy()
	left.State, right.State = "", ""
	left.AcquisitionID, right.AcquisitionID = "", ""
	left.AcquiredAt, right.AcquiredAt = metav1.Time{}, metav1.Time{}
	return reflect.DeepEqual(left, right)
}

func newTopologyLockAcquisitionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate topology lock acquisition identity: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func topologyLockAcquisitionIDValid(value string) bool {
	if len(value) != 32 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 16
}

// acquireDeviceTopologyLock publishes the status CAS before ledger Reserve.
// The shared resourceVersion makes a concurrent topology-label edit conflict;
// the caller must then revalidate this lock and all target evidence before its
// reservation CAS.
func (r *IOSXESoftwareRolloutReconciler) acquireDeviceTopologyLock(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	policyEpoch int64,
	now time.Time,
) (string, error) {
	acquisitionID, err := newTopologyLockAcquisitionID()
	if err != nil {
		return "", err
	}
	expected := expectedDeviceTopologyLock(rollout, target, policyEpoch, acquisitionID, now)
	created := false
	var recovered *ciskov1.DeviceTopologyLockStatus
	if err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var device ciskov1.CiscoDevice
		key := types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}
		if err := r.reader().Get(ctx, key, &device); err != nil {
			return fmt.Errorf("read CiscoDevice before topology lock: %w", err)
		}
		if err := validateTopologyLockTarget(&device, target); err != nil {
			return err
		}
		if device.Status.TopologyLock != nil {
			if !topologyLockCoordinatesEqual(device.Status.TopologyLock, expected) {
				return fmt.Errorf("CiscoDevice %s already has a different active topology lock", key)
			}
			if device.Status.TopologyLock.State == ciskov1.DeviceTopologyLockReleasing {
				return fmt.Errorf("CiscoDevice %s topology lock is being released", key)
			}
			if device.Status.TopologyLock.State != ciskov1.DeviceTopologyLockActive {
				return fmt.Errorf("CiscoDevice %s topology lock has invalid state %q", key, device.Status.TopologyLock.State)
			}
			recovered = device.Status.TopologyLock.DeepCopy()
			return nil
		}
		before := device.DeepCopy()
		device.Status.TopologyLock = expected.DeepCopy()
		if err := r.Client.Status().Patch(ctx, &device,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return err
		}
		created = true
		return nil
	}); err != nil {
		return "", fmt.Errorf("acquire device topology lock: %w", err)
	}
	if recovered != nil && !created {
		acquisitionID = recovered.AcquisitionID
		reservationKey := reservationID(string(rollout.UID), target.DeviceUID)
		_, ledger, err := r.ledgerStore(rollout).Read(ctx)
		if err != nil {
			return "", fmt.Errorf("read ledger while recovering device topology lock: %w", err)
		}
		if reservation, ok := ledger.Reservations[reservationKey]; ok {
			policy := rollout.Status.EffectivePolicy
			if reservation.CampaignUID != string(rollout.UID) || reservation.PlanHash != rollout.Status.FrozenPlan.Hash ||
				reservation.PolicyEpoch != policyEpoch || reservation.TopologyLockID != acquisitionID ||
				policy == nil || policy.Epoch != policyEpoch || reservation.PolicyUID != policy.Policy.UID ||
				reservation.PolicyVersion != policy.Policy.ResourceVersion ||
				reservation.PhysicalID != target.PhysicalIdentity || reservation.DeviceUID != target.DeviceUID ||
				reservation.NodeUID != target.NodeUID || reservation.ChildNamespace != rollout.Namespace ||
				reservation.ChildName != target.ChildName || !reflect.DeepEqual(reservation.Domains, targetDomains(target)) {
				return "", fmt.Errorf("%w: existing reservation does not match the device topology lock", topologyrollout.ErrLedgerIdentity)
			}
			if err := r.revalidateDeviceTopologyLock(ctx, rollout, target, policyEpoch, acquisitionID); err != nil {
				return "", err
			}
			return acquisitionID, nil
		}
		// A published Active lock without its exact reservation is an incomplete
		// prior acquisition, not authority that a later reconcile may silently
		// reuse. Persist Releasing first, then force a ledger CAS fence even when
		// the reservation is absent. Any prior Reserve already in flight must
		// conflict and rerun its lock validation before this lock can be cleared.
		if err := r.beginDeviceTopologyLockRelease(ctx, rollout, target, policyEpoch, acquisitionID); err != nil {
			return "", fmt.Errorf("retire incomplete device topology lock: %w", err)
		}
		if err := r.ledgerStore(rollout).Mutate(ctx, func(current *topologyrollout.Ledger) error {
			return topologyrollout.FenceUnboundAcquisition(current, reservationKey, policyEpoch, acquisitionID)
		}); err != nil {
			return "", fmt.Errorf("fence incomplete topology-lock acquisition: %w", err)
		}
		if err := r.releaseDeviceTopologyLock(ctx, rollout, target, policyEpoch, acquisitionID); err != nil {
			return "", fmt.Errorf("clear incomplete device topology lock: %w", err)
		}
		return "", fmt.Errorf("%w: retired incomplete topology-lock acquisition; retry admission", topologyrollout.ErrStaleControlRevision)
	}
	if err := r.revalidateDeviceTopologyLock(ctx, rollout, target, policyEpoch, acquisitionID); err != nil {
		return "", err
	}
	return acquisitionID, nil
}

// beginDeviceTopologyLockRelease wins a durable status CAS before any ledger
// deletion. An acquire that overlaps cleanup observes Releasing and cannot
// reuse the transaction, including after a manager crash or leader handoff.
func (r *IOSXESoftwareRolloutReconciler) beginDeviceTopologyLockRelease(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	policyEpoch int64,
	acquisitionID string,
) error {
	if acquisitionID == "" {
		return nil
	}
	expected := expectedDeviceTopologyLock(rollout, target, policyEpoch, acquisitionID, time.Time{})
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var device ciskov1.CiscoDevice
		key := types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}
		if err := r.reader().Get(ctx, key, &device); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("read CiscoDevice before topology lock release fence: %w", err)
		}
		lock := device.Status.TopologyLock
		if lock == nil {
			return nil
		}
		if !topologyLockAcquisitionEqual(lock, expected) {
			return fmt.Errorf("refusing to release a different CiscoDevice topology lock")
		}
		switch lock.State {
		case ciskov1.DeviceTopologyLockReleasing:
			return nil
		case ciskov1.DeviceTopologyLockActive:
			before := device.DeepCopy()
			device.Status.TopologyLock.State = ciskov1.DeviceTopologyLockReleasing
			return r.Client.Status().Patch(ctx, &device,
				client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
		default:
			return fmt.Errorf("CiscoDevice topology lock has invalid state %q", lock.State)
		}
	})
}

func (r *IOSXESoftwareRolloutReconciler) revalidateDeviceTopologyLock(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	policyEpoch int64,
	acquisitionID string,
) error {
	var device ciskov1.CiscoDevice
	key := types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}
	if err := r.reader().Get(ctx, key, &device); err != nil {
		return fmt.Errorf("read CiscoDevice topology lock: %w", err)
	}
	if err := validateTopologyLockTarget(&device, target); err != nil {
		return err
	}
	if !topologyLockIdentityEqual(device.Status.TopologyLock,
		expectedDeviceTopologyLock(rollout, target, policyEpoch, acquisitionID, time.Time{})) {
		return fmt.Errorf("CiscoDevice %s topology lock no longer matches the reservation transaction", key)
	}
	return nil
}

func validateTopologyLockTarget(device *ciskov1.CiscoDevice, target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget) error {
	if device == nil || !device.DeletionTimestamp.IsZero() || string(device.UID) != target.DeviceUID ||
		device.Generation != target.DeviceGeneration || device.Spec.Driver != ciskov1.DeviceDriverXE {
		return fmt.Errorf("CiscoDevice incarnation, generation, or driver changed before topology reservation")
	}
	if device.Status.NodeIdentity == nil || device.Status.NodeIdentity.DeviceUID != target.DeviceUID ||
		device.Status.NodeIdentity.NodeName != target.NodeName || device.Status.NodeIdentity.NodeUID != target.NodeUID {
		return fmt.Errorf("CiscoDevice Node identity changed before topology reservation")
	}
	if device.Status.TopologyProjection == nil ||
		device.Status.TopologyProjection.EffectiveLabelHash != target.ProjectionHash {
		return fmt.Errorf("CiscoDevice topology projection changed before topology reservation")
	}
	return nil
}

// topologyLockAcquisitionForRelease resolves the exact acquisition recorded by
// the durable reservation. If the reservation was already removed before a
// crash, the still-present Releasing lock supplies the same recovery identity;
// after both are gone, the caller may supply the identity retained in the leaf
// manager admission so its final settlement acknowledgement remains replayable.
func (r *IOSXESoftwareRolloutReconciler) topologyLockAcquisitionForRelease(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	expectedPolicyEpoch int64,
	expectedAcquisitionID ...string,
) (int64, string, error) {
	fallbackID := ""
	if len(expectedAcquisitionID) > 0 {
		fallbackID = expectedAcquisitionID[0]
		if fallbackID != "" && !topologyLockAcquisitionIDValid(fallbackID) {
			return 0, "", fmt.Errorf("%w: fallback topology-lock identity is invalid", topologyrollout.ErrLedgerIdentity)
		}
	}
	_, ledger, err := r.ledgerStore(rollout).Read(ctx)
	if err != nil {
		return 0, "", err
	}
	if reservation, ok := ledger.Reservations[reservationID(string(rollout.UID), target.DeviceUID)]; ok {
		if reservation.DeviceUID != target.DeviceUID || reservation.TopologyLockID == "" ||
			(expectedPolicyEpoch > 0 && reservation.PolicyEpoch != expectedPolicyEpoch) ||
			(fallbackID != "" && reservation.TopologyLockID != fallbackID) {
			return 0, "", fmt.Errorf("%w: reservation topology-lock identity changed", topologyrollout.ErrLedgerIdentity)
		}
		return reservation.PolicyEpoch, reservation.TopologyLockID, nil
	}

	var device ciskov1.CiscoDevice
	key := types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}
	if err := r.reader().Get(ctx, key, &device); err != nil {
		if apierrors.IsNotFound(err) {
			return expectedPolicyEpoch, fallbackID, nil
		}
		return 0, "", err
	}
	lock := device.Status.TopologyLock
	if lock == nil {
		return expectedPolicyEpoch, fallbackID, nil
	}
	expected := expectedDeviceTopologyLock(rollout, target, lock.PolicyEpoch, lock.AcquisitionID, time.Time{})
	if !topologyLockAcquisitionEqual(lock, expected) ||
		(expectedPolicyEpoch > 0 && lock.PolicyEpoch != expectedPolicyEpoch) ||
		(fallbackID != "" && lock.AcquisitionID != fallbackID) {
		return 0, "", fmt.Errorf("refusing to recover a different CiscoDevice topology lock")
	}
	return lock.PolicyEpoch, lock.AcquisitionID, nil
}

// releaseDeviceTopologyLock is fail-closed: it first reads the UID-bound
// ledger and refuses to clear while any reservation for this Device exists.
func (r *IOSXESoftwareRolloutReconciler) releaseDeviceTopologyLock(
	ctx context.Context,
	rollout *opsv1alpha1.IOSXESoftwareRollout,
	target opsv1alpha1.IOSXESoftwareRolloutPlannedTarget,
	policyEpoch int64,
	acquisitionID string,
) error {
	if acquisitionID == "" {
		return nil
	}
	expected := expectedDeviceTopologyLock(rollout, target, policyEpoch, acquisitionID, time.Time{})
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		// This read belongs inside the retry. The exact release-fence ledger CAS
		// has already invalidated pre-fence Reserve writes; a status conflict still
		// requires proving that no reservation was committed on the retry path.
		_, ledger, err := r.ledgerStore(rollout).Read(ctx)
		if err != nil {
			return fmt.Errorf("verify ledger before topology lock release: %w", err)
		}
		for _, reservation := range ledger.Reservations {
			if reservation.DeviceUID == target.DeviceUID {
				return fmt.Errorf("topology lock cannot be released while reservation %q remains", reservation.ID)
			}
		}
		if !topologyrollout.HasReleaseFence(ledger, expected.ReservationID, policyEpoch, acquisitionID) {
			return fmt.Errorf("topology lock cannot be cleared before its exact ledger release fence is durable")
		}
		var device ciskov1.CiscoDevice
		key := types.NamespacedName{Namespace: rollout.Namespace, Name: target.DeviceName}
		if err := r.reader().Get(ctx, key, &device); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return fmt.Errorf("read CiscoDevice before topology lock release: %w", err)
		}
		if device.Status.TopologyLock == nil {
			return nil
		}
		if !topologyLockAcquisitionEqual(device.Status.TopologyLock, expected) {
			return fmt.Errorf("refusing to release a different CiscoDevice topology lock")
		}
		if device.Status.TopologyLock.State != ciskov1.DeviceTopologyLockReleasing {
			return fmt.Errorf("refusing to clear a topology lock before its durable release fence")
		}
		before := device.DeepCopy()
		device.Status.TopologyLock = nil
		return r.Client.Status().Patch(ctx, &device,
			client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{}))
	})
}
