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

// Package operationalaction reconciles IOSXEOperationalAction CRs —
// the write-class gNOI operations. Distinct from the read-only
// DeviceOperation reconciler so RBAC can grant the two surfaces independently.
package operationalaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
	"github.com/cisco/virtual-kubelet-cisco/internal/provider/mutationguard"
)

const (
	conditionTypeReady           = "Ready"
	finalizerName                = "ops.cisco.vk/iosxeoperationalaction-finalizer"
	clientAcquisitionTimeout     = 30 * time.Second
	operationalActionRPCTimeout  = 5 * time.Minute
	actionResultPersistenceGrace = 5 * time.Second
)

var (
	provisioningCertificateIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]*$`)
	publicMaterialSHA256Pattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Reconciler dispatches IOSXEOperationalAction CRs at most once.
//
// Each kind is destructive or near-destructive, so we do NOT retry on
// transient failure. Operators must inspect device state before deciding
// whether a new CR is safe.
type Reconciler struct {
	Client          client.Client
	Reader          client.Reader
	Recorder        record.EventRecorder
	Scheme          *runtime.Scheme
	DeviceName      string
	DeviceNamespace string
	GNOI            gnoi.Provider
	// MutationLeaser serializes this write-class action with software
	// lifecycle mutations targeting the same device. CancelReboot bypasses
	// the lease so an operator can stop a scheduled reboot; it never releases
	// the holder's lease. A nil value preserves compatibility for tests and
	// embedders that do not use Kubernetes Leases.
	MutationLeaser *engine.FamilyLeaser
	// CertificateProvisioner is injected separately from GNOI so the base
	// device client cannot acquire certificate-install authority implicitly.
	// It is nil unless provisioning is explicitly configured and write-class
	// gNOI is enabled.
	CertificateProvisioner CertificateProvisioner

	// Now is injected for tests.
	Now func() time.Time
}

// CertificateProvisioner performs the IOS XE-specific, create-only
// certificate bootstrap after the reconciler has confirmed OS.Verify reports
// the exact not-provisioned state. The interface is owned by this consumer;
// implementations live in the per-device IOS XE runtime.
type CertificateProvisioner interface {
	ConfiguredIntent() (certificateID, publicMaterialSHA256 string)
	ProvisionGNOICertificate(context.Context, *gnoi.Client) (certificateID, version string, err error)
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func runningActionGraceRemaining(act *opsv1alpha1.IOSXEOperationalAction, now time.Time) time.Duration {
	if act == nil || act.Status.StartTime == nil {
		return 0
	}
	deadline := act.Status.StartTime.Time.Add(operationalActionRPCTimeout + actionResultPersistenceGrace)
	remaining := deadline.Sub(now)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func boundedActionRequeue(remaining time.Duration) time.Duration {
	if remaining < time.Second {
		return remaining
	}
	return time.Second
}

// SetupWithManager registers the IOSXEOperationalAction controller.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opsv1alpha1.IOSXEOperationalAction{}).
		Complete(r)
}

// Reconcile runs the action.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var act opsv1alpha1.IOSXEOperationalAction
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.Get(ctx, req.NamespacedName, &act); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get IOSXEOperationalAction: %w", err)
	}
	if act.Spec.DeviceRef.Name != r.DeviceName {
		return reconcile.Result{}, nil
	}
	if r.DeviceNamespace != "" && act.Namespace != r.DeviceNamespace {
		return reconcile.Result{}, nil
	}
	logger := log.G(ctx).WithField("operationalAction", req.NamespacedName.String()).
		WithField("kind", act.Spec.Action.Kind).
		WithField("device", act.Spec.DeviceRef.Name)
	now := r.now()
	isTerminal := act.Status.Phase == opsv1alpha1.ActionPhaseSucceeded ||
		act.Status.Phase == opsv1alpha1.ActionPhaseFailed ||
		act.Status.Phase == opsv1alpha1.ActionPhaseRejected
	terminalQuarantineActive := isTerminal && r.MutationLeaser != nil &&
		mutationguard.ActionRequiresQuarantineAt(&act, now)
	if terminalQuarantineActive {
		// A released controller could remove its finalizer after accepting a
		// delayed disruption, before shared mutation Leases existed. Make the
		// cleanup guard durable before seeding that compatibility fence.
		if act.DeletionTimestamp.IsZero() && !controllerutil.ContainsFinalizer(&act, finalizerName) {
			if err := r.ensureFinalizer(ctx, &act); err != nil {
				return reconcile.Result{}, err
			}
			return reconcile.Result{RequeueAfter: time.Second}, nil
		}
		owned, result, err := r.ensureCompatibilityQuarantine(ctx, &act, !act.DeletionTimestamp.IsZero(), now)
		if err != nil || !owned {
			return result, err
		}
		if act.DeletionTimestamp.IsZero() {
			// Keep the finalizer stable while this accepted/indeterminate legacy
			// action remains inside its bounded disruption window. Once it ages
			// out, the normal terminal path below releases and removes it.
			return result, nil
		}
	}

	// Terminal phases are no-ops — actions dispatch at most once.
	switch act.Status.Phase {
	case opsv1alpha1.ActionPhaseSucceeded, opsv1alpha1.ActionPhaseFailed, opsv1alpha1.ActionPhaseRejected:
		if !retainMutationLeaseUntilExpiry(&act) || !terminalQuarantineActive {
			if err := r.releaseMutationLease(ctx, &act); err != nil {
				return reconcile.Result{}, err
			}
		}
		if err := r.removeFinalizer(ctx, &act); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	if !act.DeletionTimestamp.IsZero() {
		if mutationguard.ActionRequiresQuarantine(&act) {
			owned, result, err := r.ensureCompatibilityQuarantine(ctx, &act, true, now)
			if err != nil || !owned {
				return result, err
			}
		}
		if act.Status.Phase == opsv1alpha1.ActionPhaseRunning {
			if remaining := runningActionGraceRemaining(&act, now); remaining > 0 {
				logger.WithField("invocationID", act.Status.InvocationID).
					Warn("IOSXEOperationalAction deletion requested during an in-flight invocation; preserving CR until the bounded owner window closes")
				return reconcile.Result{RequeueAfter: boundedActionRequeue(remaining)}, nil
			}
			return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, "ActionOutcomeUnknown",
				"the controller did not record the device response before the bounded action RPC window closed; the action will not be replayed", nil, now)
		}
		if act.Status.InvocationID != "" {
			logger.WithField("invocationID", act.Status.InvocationID).
				Warn("IOSXEOperationalAction deletion requested after invocation; preserving CR to avoid losing destructive-operation audit trail")
			r.recordEvent(&act, corev1.EventTypeWarning, "DeletionPending",
				"delete requested after device-side action was invoked; CR is retained for audit")
			return reconcile.Result{}, nil
		}
		return r.cleanupUninvokedDeletion(ctx, &act)
	}
	if err := r.ensureFinalizer(ctx, &act); err != nil {
		return reconcile.Result{}, err
	}
	if act.Status.Phase == opsv1alpha1.ActionPhaseRunning {
		owned, result, err := r.ensureMutationLease(ctx, &act, now)
		if err != nil || !owned {
			return result, err
		}
		if remaining := runningActionGraceRemaining(&act, now); remaining > 0 {
			logger.WithField("invocationID", act.Status.InvocationID).
				Info("IOSXEOperationalAction owner window is still active; refusing duplicate dispatch")
			return reconcile.Result{RequeueAfter: boundedActionRequeue(remaining)}, nil
		}
		logger.WithField("invocationID", act.Status.InvocationID).
			Warn("IOSXEOperationalAction owner window expired without a recorded result; terminalizing as an unknown outcome")
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, "ActionOutcomeUnknown",
			"the controller did not record the device response before the bounded action RPC window closed; the action will not be replayed", nil, now)
	}

	// Confirmation guard: prevent typo-driven actions against the wrong device.
	if act.Spec.Confirm != act.Spec.DeviceRef.Name {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseRejected, "ConfirmMismatch",
			fmt.Sprintf("spec.confirm=%q does not match spec.deviceRef.name=%q",
				act.Spec.Confirm, act.Spec.DeviceRef.Name), nil, now)
	}
	if err := validateActionRequest(act.Spec.Action); err != nil {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseRejected, "InvalidAction", err.Error(), nil, now)
	}
	if act.Spec.Action.Kind == opsv1alpha1.ActionKindProvisionCertificate {
		if r.CertificateProvisioner == nil {
			return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseRejected, "ProvisioningUnavailable",
				"gnoi certificate provisioning is not configured; enable spec.xe.gnoi.certificateProvisioning and write-class gNOI, and mount ca.key for the local signer", nil, now)
		}
		if err := validateConfiguredProvisioningIntent(act.Spec.Action.ProvisionCertificate, r.CertificateProvisioner); err != nil {
			return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseRejected, "ProvisioningIntentMismatch", err.Error(), nil, now)
		}
	}
	prepared, err := r.prepareAction(ctx, &act)
	if err != nil {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, "ActionPreparationFailed", err.Error(), nil, now)
	}

	owned, leaseResult, err := r.ensureMutationLease(ctx, &act, now)
	if err != nil || !owned {
		return leaseResult, err
	}
	now = r.now()
	if r.GNOI == nil {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, "NoGNOIProvider",
			"gnoi provider not configured on reconciler", nil, now)
	}
	clientCtx, cancelClient := context.WithTimeout(ctx, clientAcquisitionTimeout)
	gnoiClient, err := r.GNOI.GNOIClient(clientCtx)
	cancelClient()
	if err != nil {
		now = r.now()
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, "GNOIClient", err.Error(), nil, now)
	}
	now = r.now()

	// Mark Running before dispatch so the status reflects "device touched".
	claimed, deleting, err := r.markRunning(ctx, &act, now)
	if err != nil {
		return reconcile.Result{}, err
	}
	if deleting != nil {
		return r.cleanupUninvokedDeletion(ctx, deleting)
	}
	if !claimed {
		logger.Info("IOSXEOperationalAction was claimed by another reconciler; refusing duplicate dispatch")
		return reconcile.Result{}, nil
	}
	// terminal uses this durable invocation marker as its compare-and-swap
	// token. markRunning intentionally updates Kubernetes rather than the stale
	// object read at the start of reconciliation, so mirror the claimed state
	// into this reconcile's private copy before dispatch.
	act.Status.Phase = opsv1alpha1.ActionPhaseRunning
	act.Status.InvocationID = invocationID(&act)

	logger.Warn("dispatching IOSXEOperationalAction gNOI RPC")
	actionCtx, cancelAction := context.WithTimeout(ctx, operationalActionRPCTimeout)
	result, kindErr := r.dispatch(actionCtx, &act, gnoiClient, prepared)
	cancelAction()
	now = r.now()
	if kindErr != nil {
		reason := "ActionFailed"
		message := kindErr.Error()
		if gnoi.IsCertificateInstallIndeterminate(kindErr) {
			reason = "CertificateInstallIndeterminate"
			message += "; do not retry: inspect GNOICertGet and device PKI state first"
		}
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, reason, message, result, now)
	}
	successReason := "Succeeded"
	successMessage := "action completed successfully"
	if r.MutationLeaser != nil && actionMayRemainDisruptiveAfterSuccess(&act) {
		successReason = "AcceptedPendingConvergence"
		successMessage = "action accepted by device; convergence is not observed, so the device mutation lease remains quarantined until its configured TTL expires"
	}
	return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseSucceeded, successReason,
		successMessage, result, now)
}

func (r *Reconciler) cleanupUninvokedDeletion(
	ctx context.Context,
	act *opsv1alpha1.IOSXEOperationalAction,
) (reconcile.Result, error) {
	if act == nil || act.Status.InvocationID != "" {
		return reconcile.Result{}, nil
	}
	if err := r.releaseMutationLease(ctx, act); err != nil {
		return reconcile.Result{}, err
	}
	if err := r.removeFinalizer(ctx, act); err != nil {
		return reconcile.Result{}, err
	}
	return reconcile.Result{}, nil
}

func (r *Reconciler) ensureMutationLease(
	ctx context.Context,
	act *opsv1alpha1.IOSXEOperationalAction,
	now time.Time,
) (bool, reconcile.Result, error) {
	if r.MutationLeaser == nil || !actionUsesMutationLease(act) {
		return true, reconcile.Result{}, nil
	}
	guard, err := r.ensureCanonicalLegacyQuarantine(ctx, act, false, now)
	if err != nil {
		log.G(ctx).WithError(err).
			WithField("operationalAction", client.ObjectKeyFromObject(act).String()).
			Warn("legacy mutation safety scan failed; refusing device work")
		requeue, updateErr := r.updatePendingStatus(ctx, act, "LegacyMutationGuardError",
			"waiting for legacy mutation safety scan: "+err.Error(), now)
		return false, requeue, updateErr
	}
	if guard.Risk != nil {
		if guard.CallerOwnsRisk {
			// The guard already renewed the caller's Lease with the legacy risk's
			// complete safety horizon, which may exceed its normal operation TTL.
			return true, reconcile.Result{}, nil
		}
		holder := guard.ExistingHolder
		if guard.LeaseOwned || holder == "" {
			holder = guard.Risk.HolderIdentity
		}
		requeue, updateErr := r.updatePendingStatus(ctx, act, "LegacyMutationQuarantine",
			fmt.Sprintf("waiting for compatibility quarantine of %s held by %s", guard.Risk, holder), now)
		return false, requeue, updateErr
	}
	result, err := r.mutationLeaserFor(act).Acquire(
		ctx,
		devicecoordination.DeviceKey(r.DeviceNamespace, r.DeviceName),
		devicecoordination.MutationLeaseFamily,
		actionLeaseIdentity(act),
	)
	if err != nil {
		requeue, updateErr := r.updatePendingStatus(ctx, act, "MutationLeaseError",
			"waiting for the device disruptive-mutation lease: "+err.Error(), now)
		return false, requeue, updateErr
	}
	if !result.Owned {
		holder := result.Holder
		if holder == "" {
			holder = "another operation"
		}
		requeue, updateErr := r.updatePendingStatus(ctx, act, "MutationLeaseBlocked",
			fmt.Sprintf("waiting for device disruptive-mutation lease held by %s", holder), now)
		return false, requeue, updateErr
	}
	return true, reconcile.Result{}, nil
}

func (r *Reconciler) ensureCompatibilityQuarantine(
	ctx context.Context,
	act *opsv1alpha1.IOSXEOperationalAction,
	deleting bool,
	now time.Time,
) (bool, reconcile.Result, error) {
	result := reconcile.Result{RequeueAfter: 15 * time.Second}
	if r.MutationLeaser == nil {
		return false, result, errors.New("device mutation leaser is required to quarantine a legacy invoked action safely")
	}
	guard, err := r.ensureCanonicalLegacyQuarantine(ctx, act, deleting, now)
	if err != nil {
		return false, result, fmt.Errorf("acquire operational-action compatibility quarantine: %w", err)
	}
	if guard.Risk == nil {
		return false, result, errors.New("legacy invoked action was absent from the compatibility safety scan")
	}
	if !guard.CallerOwnsRisk {
		holder := guard.ExistingHolder
		if guard.LeaseOwned || holder == "" {
			holder = guard.Risk.HolderIdentity
		}
		log.G(ctx).WithField("operationalAction", client.ObjectKeyFromObject(act).String()).
			WithField("leaseHolder", holder).
			WithField("deleting", deleting).
			Warn("action is waiting for the shared mutation compatibility quarantine")
		return false, result, nil
	}
	return true, reconcile.Result{RequeueAfter: mutationguard.RenewalInterval(guard.Risk.LeaseTTL)}, nil
}

func (r *Reconciler) ensureCanonicalLegacyQuarantine(
	ctx context.Context,
	act *opsv1alpha1.IOSXEOperationalAction,
	deleting bool,
	now time.Time,
) (mutationguard.Result, error) {
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	leaser := r.MutationLeaser
	deviceKey := devicecoordination.DeviceKey(r.DeviceNamespace, r.DeviceName)
	if deleting {
		return mutationguard.EnsureCanonicalQuarantineForDeletion(ctx, reader, leaser,
			act.Namespace, r.DeviceName, deviceKey, actionLeaseIdentity(act), now)
	}
	return mutationguard.EnsureCanonicalQuarantine(ctx, reader, leaser,
		act.Namespace, r.DeviceName, deviceKey, actionLeaseIdentity(act), now)
}

func (r *Reconciler) updatePendingStatus(
	ctx context.Context,
	act *opsv1alpha1.IOSXEOperationalAction,
	reason, message string,
	now time.Time,
) (reconcile.Result, error) {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXEOperationalAction
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(act), &cur); err != nil {
			return err
		}
		if cur.Generation != act.Generation ||
			(cur.Status.Phase != "" && cur.Status.Phase != opsv1alpha1.ActionPhasePending) ||
			cur.Status.InvocationID != "" {
			return nil
		}
		cur.Status.Phase = opsv1alpha1.ActionPhasePending
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		cur.Status.ObservedGeneration = cur.Generation
		r.setReady(&cur, metav1.ConditionFalse, reason, message, now)
		return r.Client.Status().Update(ctx, &cur)
	})
	if err != nil {
		return reconcile.Result{}, fmt.Errorf("update action mutation-lease status: %w", err)
	}
	return reconcile.Result{RequeueAfter: 15 * time.Second}, nil
}

func (r *Reconciler) releaseMutationLease(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction) error {
	if r.MutationLeaser == nil || !actionUsesMutationLease(act) {
		return nil
	}
	if err := r.MutationLeaser.Release(
		ctx,
		devicecoordination.DeviceKey(r.DeviceNamespace, r.DeviceName),
		devicecoordination.MutationLeaseFamily,
		actionLeaseIdentity(act),
	); err != nil {
		return fmt.Errorf("release device disruptive-mutation lease: %w", err)
	}
	return nil
}

func actionLeaseIdentity(act *opsv1alpha1.IOSXEOperationalAction) string {
	return mutationguard.ActionHolderIdentity(act)
}

// mutationLeaserFor preserves the configured quarantine after a delayed
// reboot is due to fire. Copying avoids mutating the reconciler's shared
// leaser while concurrent reconciles compute action-specific durations.
func (r *Reconciler) mutationLeaserFor(act *opsv1alpha1.IOSXEOperationalAction) *engine.FamilyLeaser {
	leaser := *r.MutationLeaser
	if act == nil || act.Spec.Action.Kind != opsv1alpha1.ActionKindReboot || act.Spec.Action.Reboot == nil {
		return &leaser
	}
	quarantine := leaser.TTL
	if quarantine <= 0 {
		quarantine = 30 * time.Second
	}
	required := time.Duration(act.Spec.Action.Reboot.DelaySeconds)*time.Second + quarantine
	if required > leaser.TTL {
		leaser.TTL = required
	}
	return &leaser
}

func retainMutationLeaseUntilExpiry(act *opsv1alpha1.IOSXEOperationalAction) bool {
	if act == nil || !actionUsesMutationLease(act) || act.Status.InvocationID == "" {
		return false
	}
	if act.Status.Phase == opsv1alpha1.ActionPhaseFailed {
		// Once an RPC has been attempted, an error may mean the response was
		// lost after the device accepted the operation. Preserve the mutation
		// quarantine until its TTL instead of guessing that the device is idle.
		return true
	}
	if act.Status.Phase != opsv1alpha1.ActionPhaseSucceeded {
		return false
	}
	if actionMayRemainDisruptiveAfterSuccess(act) {
		// These RPCs acknowledge acceptance/scheduling, not observed device
		// convergence. Without a durable reachability/reset observer, releasing
		// here could overlap another mutation with a pending disruption.
		return true
	}
	return false
}

func actionUsesMutationLease(act *opsv1alpha1.IOSXEOperationalAction) bool {
	return act != nil && act.Spec.Action.Kind != opsv1alpha1.ActionKindCancelReboot
}

func actionMayRemainDisruptiveAfterSuccess(act *opsv1alpha1.IOSXEOperationalAction) bool {
	if act == nil {
		return false
	}
	switch act.Spec.Action.Kind {
	case opsv1alpha1.ActionKindReboot, opsv1alpha1.ActionKindFactoryReset:
		return true
	default:
		return false
	}
}

type preparedAction struct {
	filePutContent []byte
}

// prepareAction resolves Kubernetes-side dependencies before markRunning.
// Any error from this phase is definitive: no action RPC has reached the
// device and no mutation quarantine is required.
func (r *Reconciler) prepareAction(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction) (*preparedAction, error) {
	prepared := &preparedAction{}
	if act.Spec.Action.Kind != opsv1alpha1.ActionKindFilePut {
		return prepared, nil
	}
	args := act.Spec.Action.FilePut
	if args == nil {
		return nil, errors.New("action.filePut is required")
	}
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	var cm corev1.ConfigMap
	if err := reader.Get(ctx, types.NamespacedName{Namespace: act.Namespace, Name: args.ConfigMapName}, &cm); err != nil {
		return nil, fmt.Errorf("filePut: get ConfigMap %s/%s: %w", act.Namespace, args.ConfigMapName, err)
	}
	data, ok := cm.BinaryData["content"]
	if !ok {
		return nil, fmt.Errorf("filePut: ConfigMap %s/%s has no binaryData[\"content\"]", act.Namespace, args.ConfigMapName)
	}
	prepared.filePutContent = bytes.Clone(data)
	return prepared, nil
}

func (r *Reconciler) dispatch(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, gc *gnoi.Client, prepared *preparedAction) ([]byte, error) {
	kind := act.Spec.Action.Kind
	switch kind {
	case opsv1alpha1.ActionKindReboot:
		return r.runReboot(ctx, act, gc)
	case opsv1alpha1.ActionKindCancelReboot:
		return r.runCancelReboot(ctx, act, gc)
	case opsv1alpha1.ActionKindKillProcess:
		return r.runKillProcess(ctx, act, gc)
	case opsv1alpha1.ActionKindFilePut:
		return r.runFilePut(ctx, act, gc, prepared)
	case opsv1alpha1.ActionKindFileRemove:
		return r.runFileRemove(ctx, act, gc)
	case opsv1alpha1.ActionKindFactoryReset:
		return r.runFactoryReset(ctx, act, gc)
	case opsv1alpha1.ActionKindProvisionCertificate:
		return r.runProvisionCertificate(ctx, act, gc)
	default:
		return nil, fmt.Errorf("unsupported action kind %q", kind)
	}
}

func (r *Reconciler) runProvisionCertificate(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, gc *gnoi.Client) ([]byte, error) {
	intent := act.Spec.Action.ProvisionCertificate
	if intent == nil {
		return nil, fmt.Errorf("action.provisionCertificate is required")
	}
	verified, err := gc.Verify(ctx)
	if err == nil {
		return jsonResult(map[string]any{
			"status":                        "serviceAlreadyProvisioned",
			"certificateChanged":            false,
			"requestedCertificateID":        intent.CertificateID,
			"requestedPublicMaterialSHA256": intent.PublicMaterialSHA256,
			"version":                       verified.Version,
		}), nil
	}
	if !gnoi.IsDeviceNotProvisioned(err) {
		return nil, err
	}
	if r.CertificateProvisioner == nil {
		return nil, fmt.Errorf("gnoi certificate provisioning is not configured; enable spec.xe.gnoi.certificateProvisioning and write-class gNOI, and mount ca.key for the local signer: %w", err)
	}
	certificateID, version, err := r.CertificateProvisioner.ProvisionGNOICertificate(ctx, gc)
	if err != nil {
		return nil, fmt.Errorf("gnoi certificate provisioning: %w", err)
	}
	if certificateID != intent.CertificateID {
		return nil, &gnoi.ErrCertificateInstallIndeterminate{
			CertificateID: intent.CertificateID,
			Cause: fmt.Errorf(
				"gnoi certificate provisioning returned certificate ID %q; expected %q",
				certificateID,
				intent.CertificateID,
			),
		}
	}
	return jsonResult(map[string]any{
		"status":               "provisioned",
		"certificateChanged":   true,
		"certificateID":        certificateID,
		"publicMaterialSHA256": intent.PublicMaterialSHA256,
		"version":              version,
	}), nil
}

func (r *Reconciler) runReboot(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, gc *gnoi.Client) ([]byte, error) {
	args := act.Spec.Action.Reboot
	if args == nil {
		return nil, errors.New("action.reboot is required")
	}
	return nil, gc.Reboot(ctx, gnoi.RebootOpts{
		Method:  args.Method,
		Delay:   time.Duration(args.DelaySeconds) * time.Second,
		Message: args.Message,
		Force:   args.Force,
	})
}

func (r *Reconciler) runCancelReboot(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, gc *gnoi.Client) ([]byte, error) {
	msg := ""
	if a := act.Spec.Action.CancelReboot; a != nil {
		msg = a.Message
	}
	return nil, gc.CancelReboot(ctx, msg)
}

func (r *Reconciler) runKillProcess(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, gc *gnoi.Client) ([]byte, error) {
	args := act.Spec.Action.KillProcess
	if args == nil {
		return nil, errors.New("action.killProcess is required")
	}
	if args.PID == 0 && args.Name == "" {
		return nil, errors.New("action.killProcess: PID or Name is required")
	}
	return nil, gc.KillProcess(ctx, gnoi.KillProcessOpts{
		PID:     args.PID,
		Name:    args.Name,
		Signal:  args.Signal,
		Restart: args.Restart,
	})
}

func (r *Reconciler) runFilePut(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, gc *gnoi.Client, prepared *preparedAction) ([]byte, error) {
	args := act.Spec.Action.FilePut
	if args == nil {
		return nil, errors.New("action.filePut is required")
	}
	if prepared == nil {
		return nil, errors.New("filePut payload was not prepared")
	}
	return nil, gc.Put(ctx, args.Path, bytes.NewReader(prepared.filePutContent), gnoi.PutOpts{Permissions: args.Permissions})
}

func (r *Reconciler) runFileRemove(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, gc *gnoi.Client) ([]byte, error) {
	args := act.Spec.Action.FileRemove
	if args == nil {
		return nil, errors.New("action.fileRemove is required")
	}
	return nil, gc.Remove(ctx, args.Path)
}

func (r *Reconciler) runFactoryReset(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, gc *gnoi.Client) ([]byte, error) {
	args := act.Spec.Action.FactoryReset
	if args == nil {
		return nil, errors.New("action.factoryReset is required")
	}
	retainCerts := true
	if args.RetainCerts != nil {
		retainCerts = *args.RetainCerts
	}
	return nil, gc.FactoryReset(ctx, gnoi.FactoryResetOpts{
		FactoryOS:   args.FactoryOS,
		ZeroFill:    args.ZeroFill,
		RetainCerts: retainCerts,
	})
}

// --- status helpers ---

func (r *Reconciler) markRunning(
	ctx context.Context,
	act *opsv1alpha1.IOSXEOperationalAction,
	now time.Time,
) (bool, *opsv1alpha1.IOSXEOperationalAction, error) {
	claimed := false
	var deleting *opsv1alpha1.IOSXEOperationalAction
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXEOperationalAction
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(act), &cur); err != nil {
			if apierrors.IsNotFound(err) {
				if act.Status.InvocationID == "" {
					deleting = act.DeepCopy()
					deleting.DeletionTimestamp = &metav1.Time{Time: now}
				}
				return nil
			}
			return err
		}
		if !cur.DeletionTimestamp.IsZero() {
			// A fresh deleting object with no durable invocation is the only
			// state in which this stale reconcile may release the Lease it just
			// acquired. Never release on behalf of a Running peer.
			if cur.Status.InvocationID == "" {
				deleting = cur.DeepCopy()
			}
			return nil
		}
		if cur.Generation != act.Generation {
			return nil
		}
		if (cur.Status.Phase != "" && cur.Status.Phase != opsv1alpha1.ActionPhasePending) || cur.Status.InvocationID != "" {
			return nil
		}
		cur.Status.Phase = opsv1alpha1.ActionPhaseRunning
		cur.Status.StartTime = &metav1.Time{Time: now}
		cur.Status.InvocationID = invocationID(&cur)
		cur.Status.Message = "action running"
		cur.Status.ObservedGeneration = cur.Generation
		r.setReady(&cur, metav1.ConditionFalse, "Running", "action running", now)
		if err := r.Client.Status().Update(ctx, &cur); err != nil {
			return err
		}
		claimed = true
		return nil
	})
	if err == nil && claimed {
		log.G(ctx).WithField("operationalAction", client.ObjectKeyFromObject(act).String()).
			WithField("kind", act.Spec.Action.Kind).
			Info("IOSXEOperationalAction phase advanced to Running")
		recordActionTransition(act.Spec.DeviceRef.Name, string(act.Spec.Action.Kind), string(opsv1alpha1.ActionPhaseRunning), "Running")
		r.recordEvent(act, corev1.EventTypeNormal, "Running", r.actionSummary(act))
	}
	if err != nil {
		return false, nil, fmt.Errorf("claim action: %w", err)
	}
	return claimed, deleting, nil
}

func (r *Reconciler) terminal(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, phase opsv1alpha1.ActionPhase, reason, message string, result []byte, now time.Time) (reconcile.Result, error) {
	res, updated, err := r.updateStatus(ctx, act, func(cur *opsv1alpha1.IOSXEOperationalAction) {
		cur.Status.Phase = phase
		cur.Status.FailureReason = reason
		cur.Status.Message = message
		cur.Status.CompletionTime = &metav1.Time{Time: now}
		if len(result) > 0 {
			cur.Status.Result = string(result)
		}
		condStatus := metav1.ConditionFalse
		if phase == opsv1alpha1.ActionPhaseSucceeded {
			condStatus = metav1.ConditionTrue
			cur.Status.FailureReason = ""
		}
		r.setReady(cur, condStatus, reason, message, now)
	}, reconcile.Result{})
	if err != nil {
		return res, err
	}
	if !updated {
		// A concurrent reconcile advanced the action (most importantly, from
		// Pending to Running). Do not overwrite its invocation, release its
		// Lease, or remove the finalizer protecting its audit trail.
		return reconcile.Result{RequeueAfter: time.Second}, nil
	}
	var current opsv1alpha1.IOSXEOperationalAction
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(act), &current); err != nil {
		return res, fmt.Errorf("read terminal action before mutation-lease release: %w", err)
	}
	if !retainMutationLeaseUntilExpiry(&current) {
		if err := r.releaseMutationLease(ctx, &current); err != nil {
			return res, err
		}
	}
	eventType := corev1.EventTypeWarning
	if phase == opsv1alpha1.ActionPhaseSucceeded {
		eventType = corev1.EventTypeNormal
	}
	log.G(ctx).WithField("operationalAction", client.ObjectKeyFromObject(act).String()).
		WithField("kind", act.Spec.Action.Kind).
		WithField("phase", phase).
		WithField("reason", reason).
		Info("IOSXEOperationalAction reached terminal phase")
	recordActionTransition(act.Spec.DeviceRef.Name, string(act.Spec.Action.Kind), string(phase), reason)
	r.recordEvent(act, eventType, string(phase), r.actionSummary(act)+": "+message)
	if err := r.removeFinalizer(ctx, act); err != nil {
		return res, err
	}
	return res, nil
}

func (r *Reconciler) setReady(act *opsv1alpha1.IOSXEOperationalAction, status metav1.ConditionStatus, reason, message string, now time.Time) {
	apimeta.SetStatusCondition(&act.Status.Conditions, metav1.Condition{
		Type:               conditionTypeReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Time{Time: now},
		ObservedGeneration: act.Generation,
	})
}

func (r *Reconciler) updateStatus(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, mutate func(*opsv1alpha1.IOSXEOperationalAction), result reconcile.Result) (reconcile.Result, bool, error) {
	updated := false
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXEOperationalAction
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(act), &cur); err != nil {
			return err
		}
		if !actionStatusCASMatches(act, &cur) {
			return nil
		}
		mutate(&cur)
		cur.Status.ObservedGeneration = cur.Generation
		if err := r.Client.Status().Update(ctx, &cur); err != nil {
			return err
		}
		updated = true
		return nil
	})
	if err != nil {
		return result, false, fmt.Errorf("update action status: %w", err)
	}
	return result, updated, nil
}

func actionStatusCASMatches(expected, current *opsv1alpha1.IOSXEOperationalAction) bool {
	if expected == nil || current == nil || current.Generation != expected.Generation {
		return false
	}
	if expected.Status.InvocationID != "" {
		return expected.Status.Phase == opsv1alpha1.ActionPhaseRunning &&
			current.Status.Phase == opsv1alpha1.ActionPhaseRunning &&
			current.Status.InvocationID == expected.Status.InvocationID
	}
	return (expected.Status.Phase == "" || expected.Status.Phase == opsv1alpha1.ActionPhasePending) &&
		(current.Status.Phase == "" || current.Status.Phase == opsv1alpha1.ActionPhasePending) &&
		current.Status.InvocationID == ""
}

func (r *Reconciler) ensureFinalizer(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction) error {
	if controllerutil.ContainsFinalizer(act, finalizerName) {
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXEOperationalAction
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(act), &cur); err != nil {
			return err
		}
		if controllerutil.ContainsFinalizer(&cur, finalizerName) {
			return nil
		}
		controllerutil.AddFinalizer(&cur, finalizerName)
		return r.Client.Update(ctx, &cur)
	})
}

func (r *Reconciler) removeFinalizer(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction) error {
	return retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXEOperationalAction
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(act), &cur); err != nil {
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		}
		if !controllerutil.ContainsFinalizer(&cur, finalizerName) {
			return nil
		}
		controllerutil.RemoveFinalizer(&cur, finalizerName)
		return r.Client.Update(ctx, &cur)
	})
}

func validateActionRequest(action opsv1alpha1.ActionRequest) error {
	argsBlocks := 0
	if action.Reboot != nil {
		argsBlocks++
	}
	if action.CancelReboot != nil {
		argsBlocks++
	}
	if action.KillProcess != nil {
		argsBlocks++
	}
	if action.FilePut != nil {
		argsBlocks++
	}
	if action.FileRemove != nil {
		argsBlocks++
	}
	if action.FactoryReset != nil {
		argsBlocks++
	}
	if action.ProvisionCertificate != nil {
		argsBlocks++
	}
	if argsBlocks != 1 {
		return fmt.Errorf("action.kind %q requires exactly one matching args block; got %d", action.Kind, argsBlocks)
	}
	switch action.Kind {
	case opsv1alpha1.ActionKindReboot:
		if action.Reboot == nil {
			return errors.New("action.kind Reboot requires action.reboot")
		}
		switch action.Reboot.Method {
		case "", "COLD", "NSF", "POWERDOWN", "HALT", "WARM":
		default:
			return fmt.Errorf("action.reboot.method %q is unsupported", action.Reboot.Method)
		}
		if action.Reboot.DelaySeconds < 0 || action.Reboot.DelaySeconds > 604800 {
			return errors.New("action.reboot.delaySeconds must be between 0 and 604800")
		}
	case opsv1alpha1.ActionKindCancelReboot:
		if action.CancelReboot == nil {
			return errors.New("action.kind CancelReboot requires action.cancelReboot")
		}
	case opsv1alpha1.ActionKindKillProcess:
		if action.KillProcess == nil {
			return errors.New("action.kind KillProcess requires action.killProcess")
		}
		if action.KillProcess.PID == 0 && action.KillProcess.Name == "" {
			return errors.New("action.killProcess: PID or Name is required")
		}
		switch action.KillProcess.Signal {
		case "", "TERM", "KILL", "HUP", "ABRT":
		default:
			return fmt.Errorf("action.killProcess.signal %q is unsupported", action.KillProcess.Signal)
		}
	case opsv1alpha1.ActionKindFilePut:
		if action.FilePut == nil {
			return errors.New("action.kind FilePut requires action.filePut")
		}
		if action.FilePut.ConfigMapName == "" {
			return errors.New("action.filePut.configMapName is required")
		}
		if err := gnoi.ValidateIOSXEPath(action.FilePut.Path); err != nil {
			return fmt.Errorf("action.filePut.path: %w", err)
		}
		if action.FilePut.Permissions > 0o777 {
			return fmt.Errorf("action.filePut.permissions=%#o exceeds 0777", action.FilePut.Permissions)
		}
	case opsv1alpha1.ActionKindFileRemove:
		if action.FileRemove == nil {
			return errors.New("action.kind FileRemove requires action.fileRemove")
		}
		if err := gnoi.ValidateIOSXEPath(action.FileRemove.Path); err != nil {
			return fmt.Errorf("action.fileRemove.path: %w", err)
		}
	case opsv1alpha1.ActionKindFactoryReset:
		if action.FactoryReset == nil {
			return errors.New("action.kind FactoryReset requires action.factoryReset")
		}
	case opsv1alpha1.ActionKindProvisionCertificate:
		if action.ProvisionCertificate == nil {
			return errors.New("action.kind ProvisionCertificate requires action.provisionCertificate")
		}
		if id := action.ProvisionCertificate.CertificateID; len(id) > 64 || !provisioningCertificateIDPattern.MatchString(id) {
			return errors.New("action.provisionCertificate.certificateID must start with an alphanumeric and contain 1-64 characters from [A-Za-z0-9_.-]")
		}
		if !publicMaterialSHA256Pattern.MatchString(action.ProvisionCertificate.PublicMaterialSHA256) {
			return errors.New("action.provisionCertificate.publicMaterialSHA256 must be exactly 64 lowercase hexadecimal characters")
		}
	default:
		return fmt.Errorf("unsupported action kind %q", action.Kind)
	}
	return nil
}

func validateConfiguredProvisioningIntent(intent *opsv1alpha1.ProvisionCertificateActionArgs, provisioner CertificateProvisioner) error {
	configuredID, configuredDigest := provisioner.ConfiguredIntent()
	if intent.CertificateID == configuredID && intent.PublicMaterialSHA256 == configuredDigest {
		return nil
	}
	return fmt.Errorf(
		"requested gNOI provisioning intent certificateID=%q publicMaterialSHA256=%q does not match worker configuration certificateID=%q publicMaterialSHA256=%q",
		intent.CertificateID,
		intent.PublicMaterialSHA256,
		configuredID,
		configuredDigest,
	)
}

func invocationID(act *opsv1alpha1.IOSXEOperationalAction) string {
	if act.UID != "" {
		return fmt.Sprintf("%s/%d", act.UID, act.Generation)
	}
	return fmt.Sprintf("%s/%s/%d", act.Namespace, act.Name, act.Generation)
}

func (r *Reconciler) recordEvent(act *opsv1alpha1.IOSXEOperationalAction, eventType, reason, message string) {
	if r.Recorder == nil {
		return
	}
	r.Recorder.Event(act, eventType, reason, message)
}

func (r *Reconciler) actionSummary(act *opsv1alpha1.IOSXEOperationalAction) string {
	action := act.Spec.Action
	base := fmt.Sprintf("%s device=%s", action.Kind, act.Spec.DeviceRef.Name)
	switch action.Kind {
	case opsv1alpha1.ActionKindReboot:
		if action.Reboot == nil {
			return base
		}
		return fmt.Sprintf("%s method=%s delaySeconds=%d force=%t", base, action.Reboot.Method, action.Reboot.DelaySeconds, action.Reboot.Force)
	case opsv1alpha1.ActionKindCancelReboot:
		return base
	case opsv1alpha1.ActionKindKillProcess:
		if action.KillProcess == nil {
			return base
		}
		return fmt.Sprintf("%s pid=%d name=%q signal=%s restart=%t", base, action.KillProcess.PID, action.KillProcess.Name, action.KillProcess.Signal, action.KillProcess.Restart)
	case opsv1alpha1.ActionKindFilePut:
		if action.FilePut == nil {
			return base
		}
		return fmt.Sprintf("%s path=%s configMap=%s", base, action.FilePut.Path, action.FilePut.ConfigMapName)
	case opsv1alpha1.ActionKindFileRemove:
		if action.FileRemove == nil {
			return base
		}
		return fmt.Sprintf("%s path=%s", base, action.FileRemove.Path)
	case opsv1alpha1.ActionKindFactoryReset:
		if action.FactoryReset == nil {
			return base
		}
		retain := "default"
		if action.FactoryReset.RetainCerts != nil {
			retain = fmt.Sprintf("%t", *action.FactoryReset.RetainCerts)
		}
		return fmt.Sprintf("%s factoryOS=%t zeroFill=%t retainCerts=%s", base, action.FactoryReset.FactoryOS, action.FactoryReset.ZeroFill, retain)
	default:
		return base
	}
}

// jsonResult marshals device-side output for status.result.
func jsonResult(payload any) []byte {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return b
}
