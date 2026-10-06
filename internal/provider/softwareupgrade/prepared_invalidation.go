// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwareupgrade

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"github.com/cisco/virtual-kubelet-cisco/internal/workloaddrain"
)

func PreparedInvalidationRequestHash(request *ops.UpgradePreparedInvalidationRequest) string {
	data, _ := json.Marshal(request) // closed API struct, no fallible custom marshaler
	return fmt.Sprintf("sha256:%x", sha256.Sum256(data))
}

// ValidatePreparedInvalidationRequest is shared by manager and worker. Only
// conclusively settled, cancelled preparation can enter this observation-only
// path. It never reconciles an uncertain install or activation by fiat.
func ValidatePreparedInvalidationRequest(up *ops.IOSXESoftwareUpgrade) error {
	if up == nil || up.Spec.Strategy != ops.UpgradeStrategyPrepareOnly ||
		(up.Status.Phase != ops.UpgradePhasePrepared && up.Status.Phase != ops.UpgradePhasePreparedInvalidated) ||
		up.Status.ExecutionModel != ops.UpgradeExecutionModelAtMostOnceV1 ||
		up.Status.CompletionTime == nil || up.Status.ManagerInvalidation == nil ||
		up.Status.PreparedReceipt == nil || ValidatePreparedReceipt(up.Status.PreparedReceipt) != nil {
		return fmt.Errorf("invalidation requires an exact, completed preparation receipt")
	}
	receipt, request := up.Status.PreparedReceipt, up.Status.ManagerInvalidation
	a, c, w := up.Status.ManagerAdmission, up.Status.ManagerControl, up.Status.WorkerControl
	if !ops.ManagedUpgradeProtocolMatches(up) || a == nil || c == nil || w == nil ||
		a.State != ops.UpgradeManagerAdmissionSettled || c.Pause ||
		w.EffectiveState != ops.UpgradeWorkerControlSettled || w.ObservedAdmissionState != a.State ||
		w.ObservedControlRevision != c.Revision || w.ObservedPolicyEpoch != a.PolicyEpoch ||
		!meta.IsStatusConditionTrue(up.Status.Conditions, conditionTypeMutationSettled) ||
		up.Status.PrimarySupervisorActivationRequested || up.Status.StandbySupervisorActivationRequested ||
		up.Status.RollbackActivationRequested || up.Status.NoRebootActivationAccepted ||
		up.Status.ActivationStartTime != nil || up.Status.ActivationControlStartTime != nil ||
		up.Status.RollbackStartTime != nil || up.Status.StandbySupervisorActivated ||
		!up.Status.PrimarySupervisorInstalled ||
		!reflect.DeepEqual(receipt.ManagedMutationClaims, up.Status.ManagedMutationClaims) {
		return fmt.Errorf("invalidation requires acknowledged cancellation and conclusively settled preparation-only claims")
	}
	controlAuthorized := c.Cancel && request.ControlRevision <= c.Revision
	if drain := up.Status.ManagerDrain; drain != nil {
		// A terminal drain's control revision is immutable recovery evidence.
		// The manager separately authorizes retirement from the cancelled parent;
		// a newer retirement request must not rearm or rewrite that old session.
		if drain.ProtocolVersion != ops.ManagedDrainProtocolPDBV1 || drain.State != ops.UpgradeManagerDrainSettled ||
			drain.SessionToken == "" || drain.NodeUID != a.NodeUID || drain.ReservationID != a.ReservationID ||
			drain.PolicyEpoch != a.PolicyEpoch || drain.ControlRevision != c.Revision ||
			a.ControlRevision == nil || *a.ControlRevision != drain.ControlRevision ||
			drain.StartedAt.IsZero() || drain.UpdatedAt.Before(&drain.StartedAt) ||
			drain.RecoveryDeadline == nil || !drain.RecoveryDeadline.After(drain.StartedAt.Time) {
			return fmt.Errorf("invalidation requires exact settled drain authority")
		}
		for i := range drain.Pods {
			pod := &drain.Pods[i]
			if pod.UID == "" || pod.Phase != ops.UpgradeDrainPodComplete || workloaddrain.VerifyEligibilityHash(pod) != nil {
				return fmt.Errorf("invalidation requires complete immutable drain Pod evidence")
			}
		}
		controlAuthorized = controlAuthorized || request.ControlRevision > c.Revision
	}
	if !controlAuthorized {
		return fmt.Errorf("invalidation requires cancelled control or separately authorized retirement of an exact settled drain")
	}
	if receipt.UpgradeUID != string(up.UID) || a.LeafUID != string(up.UID) ||
		receipt.DeviceUID != a.DeviceUID || receipt.NodeUID != a.NodeUID ||
		receipt.PhysicalIdentity != a.PhysicalIdentity || receipt.DeviceGeneration != a.DeviceGeneration ||
		receipt.CampaignUID != a.CampaignUID || receipt.PlanHash != a.PlanHash ||
		request.ReceiptHash != receipt.ReceiptHash || request.PlanHash != receipt.PlanHash ||
		request.CampaignUID != receipt.CampaignUID ||
		request.ControlRevision < 1 || request.RequestedAt.IsZero() ||
		request.RequestedAt.Before(&receipt.PreparedAt) ||
		strings.TrimSpace(request.RequestedBy) == "" || strings.TrimSpace(request.Reason) == "" {
		return fmt.Errorf("invalidation authority does not bind this receipt and cancelled control revision")
	}
	return validateManagedClaimCoverage(up, c.Revision)
}

// PreparedReceiptInvalidated is the sole additional ownership-release
// predicate. A phase or cancellation alone cannot retire retained ownership.
func PreparedReceiptInvalidated(up *ops.IOSXESoftwareUpgrade) bool {
	if ValidatePreparedInvalidationRequest(up) != nil || up.Status.Phase != ops.UpgradePhasePreparedInvalidated {
		return false
	}
	proof := up.Status.PreparedInvalidation
	return proof != nil && proof.RequestHash == PreparedInvalidationRequestHash(up.Status.ManagerInvalidation) &&
		validManagedSHA256(proof.NativeEvidenceHash) && validManagedSHA256(proof.WorkerRevision) &&
		strings.TrimSpace(proof.WorkerPodUID) != "" && !proof.ObservedAt.IsZero() &&
		!proof.ObservedAt.Before(&up.Status.ManagerInvalidation.RequestedAt) &&
		proof.RunningVersion == up.Status.PreparedReceipt.RunningVersion &&
		(proof.TargetState == string(softwarelifecycle.InventoryStateAbsent) ||
			proof.TargetState == string(softwarelifecycle.InventoryStateInstalled))
}

func (r *Reconciler) reconcilePreparedInvalidation(ctx context.Context, up *ops.IOSXESoftwareUpgrade) (result reconcile.Result, retErr error) {
	wait := reconcile.Result{RequeueAfter: managedAdmissionPoll}
	if err := ValidatePreparedInvalidationRequest(up); err != nil {
		return wait, err
	}
	observer, ok := r.Lifecycle.(softwarelifecycle.PreparationRetirementObserver)
	if !ok || r.MutationLeaser == nil {
		return wait, fmt.Errorf("prepared invalidation requires a native retirement observer and device mutation lease")
	}
	// Bound the entire observation/publication attempt to less than one lease
	// lifetime. Reacquiring an expired lease must never legitimize old reads.
	ttl := r.MutationLeaser.TTL
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	budget := min(controlRPCTimeout, ttl/2)
	callCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	// Do not invoke the mutation/drain preparation callback: this workflow
	// only observes. Hold the normal device-wide lease to exclude CVK writes
	// while native evidence and the status transition are acquired.
	guard, err := r.ensureCanonicalLegacyQuarantine(callCtx, up, false, r.now())
	if err != nil || guard.Risk != nil {
		return wait, fmt.Errorf("prepared invalidation is blocked by unresolved device mutation evidence: %v", err)
	}
	lease, err := r.MutationLeaser.Acquire(callCtx, r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily, upgradeLeaseIdentity(up))
	if err != nil {
		return wait, err
	}
	if !lease.Owned {
		return wait, nil
	}
	// This path never dispatches a write. Its own observation lease can always
	// be released; it must never release a foreign or uncertain mutation lease.
	defer func() {
		if err := r.releaseMutationLease(ctx, up); err != nil && retErr == nil {
			retErr = fmt.Errorf("release observation lease: %w", err)
		}
	}()
	receipt := up.Status.PreparedReceipt
	native, err := observer.ObservePreparationRetirement(callCtx, softwarelifecycle.PreparationRetirementRequest{
		TargetVersion: receipt.ValidatedVersion, RunningVersion: receipt.RunningVersion,
		SourceSize: receipt.SourceSize, InstallStartedAt: receipt.InstallStartedAt.Time,
		PreparedAt: receipt.PreparedAt.Time,
	})
	if err != nil {
		return wait, fmt.Errorf("native preparation retirement is not proven: %w", err)
	}
	gnoiClient, err := r.gnoiClient(callCtx)
	if err != nil {
		return wait, err
	}
	verify, err := verifyOS(callCtx, gnoiClient)
	if err != nil || verify == nil || verify.Version != up.Status.PreparedReceipt.RunningVersion ||
		verify.ActivationFailMessage != "" || verify.IndividualSupervisorInstall ||
		up.Status.PreparedReceipt.IndividualSupervisorInstall {
		return wait, fmt.Errorf("prepared invalidation requires unchanged running OS and a qualified single-supervisor receipt")
	}
	var current ops.IOSXESoftwareUpgrade
	if err := r.apiReader().Get(callCtx, client.ObjectKeyFromObject(up), &current); err != nil {
		return wait, err
	}
	if current.UID != up.UID || !upgradeStatusCASMatches(up, &current) ||
		ValidatePreparedInvalidationRequest(&current) != nil {
		return wait, nil
	}
	if err := r.validateManagedLeafBinding(callCtx, &current); err != nil {
		return wait, err
	}
	// Renew/check exclusive ownership immediately before publishing evidence.
	lease, err = r.MutationLeaser.AcquireIfFree(callCtx, r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily, upgradeLeaseIdentity(up))
	if err != nil || !lease.Owned {
		return wait, err
	}
	now := r.now()
	current.Status.Phase = ops.UpgradePhasePreparedInvalidated
	current.Status.PreparedInvalidation = &ops.UpgradePreparedInvalidationStatus{
		RequestHash:        PreparedInvalidationRequestHash(current.Status.ManagerInvalidation),
		NativeEvidenceHash: native.EvidenceHash, ObservedAt: metav1.NewTime(now),
		WorkerRevision: r.WorkerRevision, WorkerPodUID: r.WorkerPodUID,
		RunningVersion: verify.Version, TargetState: string(native.TargetState),
	}
	if !PreparedReceiptInvalidated(&current) {
		return wait, fmt.Errorf("native invalidation evidence is incomplete")
	}
	if err := callCtx.Err(); err != nil {
		return wait, err
	}
	current.Status.Message = "cancelled preparation invalidated with native quiescence proof; original receipt retained; no device mutation performed"
	r.setReady(&current, metav1.ConditionFalse, "PreparedInvalidated", current.Status.Message, now)
	if err := r.Client.Status().Update(callCtx, &current); err != nil {
		return wait, err // reconcile again with fresh native evidence, never replay
	}
	return reconcile.Result{}, nil
}
