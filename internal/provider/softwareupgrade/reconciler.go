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

package softwareupgrade

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
)

// SetupWithManager registers the IOSXESoftwareUpgrade controller.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		Complete(r)
}

// Finalizer is held while an upgrade is in flight so a delete cannot
// orphan device-side resources (in-flight Install streams, staged
// image bytes).
const Finalizer = "ops.cisco.vk/iosxesoftwareupgrade-cleanup"

const (
	conditionTypeReady           = "Ready"
	conditionTypeImageResolved   = "ImageResolved"
	conditionTypeTransferred     = "Transferred"
	conditionTypeValidated       = "Validated"
	conditionTypeActivated       = "Activated"
	conditionTypeDeviceReachable = "DeviceReachable"
	conditionTypeVerified        = "Verified"
)

const (
	awaitingReachabilityPoll = 30 * time.Second
)

// GNOIProvider exposes the per-device gNOI client. Set on the
// reconciler at startup.
type GNOIProvider interface {
	GNOIClient(ctx context.Context) (*gnoi.Client, error)
}

// TransportProvider exposes the per-device config transport for
// install-oper reads that complement gNOI lifecycle RPCs.
type TransportProvider interface {
	GetTransport() transport.Interface
}

// Reconciler advances IOSXESoftwareUpgrade CRs through the upgrade
// state machine. Exactly one transition per Reconcile call.
type Reconciler struct {
	Client          client.Client
	Reader          client.Reader
	Recorder        record.EventRecorder
	Scheme          *runtime.Scheme
	DeviceName      string
	DeviceNamespace string
	GNOI            GNOIProvider
	TP              TransportProvider
	ImageResolver   ImageResolver

	// Now is injected for tests. nil means time.Now.
	Now func() time.Time
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// Reconcile is the main entry point. Defensive guards run first, then
// the per-phase dispatcher.
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (reconcile.Result, error) {
	var up opsv1alpha1.IOSXESoftwareUpgrade
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.Get(ctx, req.NamespacedName, &up); err != nil {
		if apierrors.IsNotFound(err) {
			return reconcile.Result{}, nil
		}
		return reconcile.Result{}, fmt.Errorf("get IOSXESoftwareUpgrade: %w", err)
	}

	// Defensive: ignore upgrades that target a different device or live
	// in a different namespace than the one this reconciler serves.
	if up.Spec.DeviceRef.Name != r.DeviceName {
		return reconcile.Result{}, nil
	}
	if r.DeviceNamespace != "" && up.Namespace != r.DeviceNamespace {
		return reconcile.Result{}, nil
	}

	now := r.now()

	// Deletion path.
	if !up.DeletionTimestamp.IsZero() {
		return r.handleDelete(ctx, &up, now)
	}

	// Add finalizer on first observe.
	if !controllerutil.ContainsFinalizer(&up, Finalizer) {
		controllerutil.AddFinalizer(&up, Finalizer)
		if err := r.Client.Update(ctx, &up); err != nil {
			return reconcile.Result{}, fmt.Errorf("set finalizer: %w", err)
		}
		return reconcile.Result{RequeueAfter: time.Second}, nil
	}

	switch up.Status.Phase {
	case "", opsv1alpha1.UpgradePhasePending:
		return r.runPending(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseResolving:
		return r.runResolving(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseTransferring:
		return r.runTransferring(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseTransferInterrupted:
		return r.runTransferInterrupted(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseValidating:
		// Validating is folded into runTransferring — the gNOI stream
		// returns Validated as its terminal success message. If we
		// ever land here, advance to Activating.
		return r.advance(ctx, &up, opsv1alpha1.UpgradePhaseActivating, "Validating", "image validated, activating")
	case opsv1alpha1.UpgradePhaseActivating:
		return r.runActivating(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseAwaitingReachability:
		return r.runAwaitingReachability(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseVerifying:
		return r.runVerifying(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseRollingBack:
		// Phase C ships RollingBack as a terminal placeholder — the
		// reconciler records the rollback decision but does not yet
		// trigger the boot-variable rewrite. Phase D follow-up wires
		// the gNMI Set + System.Reboot pair.
		return r.terminal(ctx, &up, opsv1alpha1.UpgradePhaseRolledBack, "RolledBack",
			"rollback pending: boot-variable rewrite not yet implemented in Phase C", now)
	default:
		// Terminal phases: nothing to do.
		return reconcile.Result{}, nil
	}
}

// --- per-phase handlers ---

func (r *Reconciler) runPending(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	if up.Spec.MaintenanceWindow != nil {
		w := up.Spec.MaintenanceWindow
		if w.NotBefore != nil && now.Before(w.NotBefore.Time) {
			return r.pendingMessage(ctx, up, "WindowPending",
				fmt.Sprintf("waiting for maintenance window start (%s)", w.NotBefore.Time.Format(time.RFC3339)),
				now, 30*time.Second)
		}
		if w.NotAfter != nil && now.After(w.NotAfter.Time) {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "MaintenanceWindowExpired",
				"maintenance window NotAfter has passed", now)
		}
	}

	// Preflight: ImageSource must declare exactly one variant. The CRD-
	// level validation enforces field-level patterns but cannot encode
	// "exactly one of"; check here.
	if err := validateImageSource(up.Spec.ImageSource); err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "InvalidImageSource", err.Error(), now)
	}

	return r.advance(ctx, up, opsv1alpha1.UpgradePhaseResolving, "Resolving", "preflight passed, resolving image")
}

func (r *Reconciler) runResolving(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	if r.GNOI != nil {
		client, err := r.GNOI.GNOIClient(ctx)
		if err != nil {
			return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				if cur.Status.StartTime == nil {
					cur.Status.StartTime = &metav1.Time{Time: now}
				}
				cur.Status.Phase = opsv1alpha1.UpgradePhaseResolving
				cur.Status.Message = fmt.Sprintf("waiting for gNOI OS.Verify before image resolution: %s", err.Error())
				cur.Status.FailureReason = ""
				r.setReady(cur, metav1.ConditionFalse, "DeviceUnreachable", cur.Status.Message, now)
			}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
		}
		if res, err := client.Verify(ctx); err != nil {
			return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				if cur.Status.StartTime == nil {
					cur.Status.StartTime = &metav1.Time{Time: now}
				}
				cur.Status.Phase = opsv1alpha1.UpgradePhaseResolving
				cur.Status.Message = fmt.Sprintf("waiting for gNOI OS.Verify before image resolution: %s", err.Error())
				cur.Status.FailureReason = ""
				r.setReady(cur, metav1.ConditionFalse, "VerifyPending", cur.Status.Message, now)
			}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
		} else if versionMatches(res.Version, up.Spec.TargetVersion) {
			return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				if cur.Status.StartTime == nil {
					cur.Status.StartTime = &metav1.Time{Time: now}
				}
				cur.Status.Phase = opsv1alpha1.UpgradePhaseSucceeded
				cur.Status.RunningVersion = res.Version
				cur.Status.Message = fmt.Sprintf("target version %s already running; image transfer skipped", res.Version)
				cur.Status.FailureReason = ""
				cur.Status.CompletionTime = &metav1.Time{Time: now}
				r.setCondition(cur, conditionTypeImageResolved, metav1.ConditionTrue, "AlreadyRunning",
					"image resolution skipped because the device is already on the target version", now)
				r.setCondition(cur, conditionTypeTransferred, metav1.ConditionTrue, "AlreadyRunning",
					"image transfer skipped because the device is already on the target version", now)
				r.setCondition(cur, conditionTypeActivated, metav1.ConditionTrue, "AlreadyRunning",
					"activation skipped because the device is already on the target version", now)
				r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionTrue, "AlreadyRunning",
					fmt.Sprintf("device is reachable on target version %s", res.Version), now)
				r.setCondition(cur, conditionTypeVerified, metav1.ConditionTrue, "AlreadyRunning",
					fmt.Sprintf("gNOI OS.Verify reports target version %s", res.Version), now)
				r.setReady(cur, metav1.ConditionTrue, "AlreadyRunning", cur.Status.Message, now)
			}, reconcile.Result{})
		}
	}
	if r.ImageResolver == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "NoImageResolver",
			"image resolver not configured on reconciler", now)
	}
	if stagedVersion, err := r.resolveStagedVersion(ctx, up.Spec.TargetVersion); err == nil && stagedVersion != "" {
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			if cur.Status.StartTime == nil {
				cur.Status.StartTime = &metav1.Time{Time: now}
			}
			cur.Status.Phase = opsv1alpha1.UpgradePhaseActivating
			cur.Status.ValidatedVersion = stagedVersion
			cur.Status.Message = fmt.Sprintf("device already has staged version %s, activating", stagedVersion)
			cur.Status.FailureReason = ""
			r.setCondition(cur, conditionTypeImageResolved, metav1.ConditionTrue, "StagedVersionResolved",
				fmt.Sprintf("target version %s is already staged on the device", stagedVersion), now)
			r.setCondition(cur, conditionTypeTransferred, metav1.ConditionTrue, "StagedVersionResolved",
				"image transfer skipped because the target version is already staged", now)
			r.setCondition(cur, conditionTypeValidated, metav1.ConditionTrue, "StagedVersionResolved",
				fmt.Sprintf("device reports staged target version %s", stagedVersion), now)
			r.setCondition(cur, conditionTypeActivated, metav1.ConditionFalse, "ActivationPending",
				"waiting to submit gNOI OS.Activate", now)
			r.setReady(cur, metav1.ConditionFalse, "StagedVersionResolved", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: time.Second})
	}
	resolved, err := r.ImageResolver.Resolve(ctx, up.Namespace, up.Spec.ImageSource)
	if err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ImageResolveFailed", err.Error(), now)
	}
	// LocalPath shortcut: image is already on flash, skip Transferring.
	if resolved.Local {
		if resolved.Cleanup != nil {
			_ = resolved.Cleanup()
		}
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			if cur.Status.StartTime == nil {
				cur.Status.StartTime = &metav1.Time{Time: now}
			}
			cur.Status.Phase = opsv1alpha1.UpgradePhaseActivating
			cur.Status.Message = "using image already on device flash"
			cur.Status.FailureReason = ""
			r.setCondition(cur, conditionTypeImageResolved, metav1.ConditionTrue, "LocalPath",
				"image source is already present on device flash", now)
			r.setCondition(cur, conditionTypeTransferred, metav1.ConditionTrue, "LocalPath",
				"image transfer skipped for localPath source", now)
			r.setCondition(cur, conditionTypeActivated, metav1.ConditionFalse, "ActivationPending",
				"waiting to submit gNOI OS.Activate", now)
			r.setReady(cur, metav1.ConditionFalse, "ImageResolved", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: time.Second})
	}
	// Phase C invariant: bulk-transfer streams happen inline within
	// runTransferring. We do not stash the resolved image on the CR;
	// runTransferring re-resolves and streams in one phase. This
	// keeps the state machine durable across pod restarts at the cost
	// of one HTTP GET per Transferring entry.
	if resolved.Cleanup != nil {
		_ = resolved.Cleanup()
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		if cur.Status.StartTime == nil {
			cur.Status.StartTime = &metav1.Time{Time: now}
		}
		cur.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		cur.Status.Message = fmt.Sprintf("image resolved (%d bytes), transferring to device", resolved.Size)
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeImageResolved, metav1.ConditionTrue, "ImageResolved",
			fmt.Sprintf("image source resolved to %d bytes", resolved.Size), now)
		r.setCondition(cur, conditionTypeTransferred, metav1.ConditionFalse, "TransferPending",
			"waiting to stream image bytes with gNOI OS.Install", now)
		r.setReady(cur, metav1.ConditionFalse, "ImageResolved", cur.Status.Message, now)
	}, reconcile.Result{RequeueAfter: time.Second})
}

func (r *Reconciler) runTransferring(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	if r.GNOI == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "NoGNOIProvider",
			"gnoi provider not configured on reconciler", now)
	}
	client, err := r.GNOI.GNOIClient(ctx)
	if err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "GNOIClient", err.Error(), now)
	}
	if _, err := client.Verify(ctx); err != nil {
		return r.handleInstallErr(ctx, up, fmt.Errorf("gnoi OS.Verify preflight: %w", err), now)
	}
	resolved, err := r.ImageResolver.Resolve(ctx, up.Namespace, up.Spec.ImageSource)
	if err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ImageResolveFailed", err.Error(), now)
	}
	defer func() {
		if resolved != nil && resolved.Cleanup != nil {
			_ = resolved.Cleanup()
		}
	}()
	if resolved.Local {
		// Shouldn't happen — Resolving handles LocalPath. Defensive.
		return r.advance(ctx, up, opsv1alpha1.UpgradePhaseActivating, "ImageResolved",
			"image already on device flash")
	}
	progress, err := client.Install(ctx, resolved.Reader, gnoi.InstallOpts{
		Version:     up.Spec.TargetVersion,
		PackageSize: uint64(resolved.Size),
	})
	if err != nil {
		return r.handleInstallErr(ctx, up, err, now)
	}
	var validated *gnoi.InstallValidated
	for ev := range progress {
		switch {
		case ev.Err != nil:
			return r.handleInstallErr(ctx, up, ev.Err, now)
		case ev.TransferProgress != nil:
			r.updateTransferProgress(ctx, up, ev.TransferProgress.BytesReceived, resolved.Size, now)
		case ev.Validated != nil:
			validated = ev.Validated
		}
	}
	if validated == nil {
		return r.handleInstallErr(ctx, up, errors.New("gnoi Install: stream ended without Validated"), now)
	}
	if validated.Version != "" && !versionMatches(validated.Version, up.Spec.TargetVersion) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "VersionMismatch",
			fmt.Sprintf("device validated version %q but spec targets %q", validated.Version, up.Spec.TargetVersion), now)
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		if cur.Status.StartTime == nil {
			cur.Status.StartTime = &metav1.Time{Time: now}
		}
		cur.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		cur.Status.ValidatedVersion = validated.Version
		cur.Status.Message = fmt.Sprintf("device validated %s, activating", validated.Version)
		cur.Status.FailureReason = ""
		markTransferComplete(cur)
		r.setCondition(cur, conditionTypeTransferred, metav1.ConditionTrue, "Transferred",
			"image transfer completed and the install stream closed cleanly", now)
		r.setCondition(cur, conditionTypeValidated, metav1.ConditionTrue, "Validated",
			fmt.Sprintf("device validated image version %s", validated.Version), now)
		r.setCondition(cur, conditionTypeActivated, metav1.ConditionFalse, "ActivationPending",
			"waiting to submit gNOI OS.Activate", now)
		r.setReady(cur, metav1.ConditionFalse, "Validated", cur.Status.Message, now)
	}, reconcile.Result{RequeueAfter: time.Second})
}

func (r *Reconciler) handleInstallErr(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, err error, now time.Time) (reconcile.Result, error) {
	// INSTALL_IN_PROGRESS and transient stream errors → TransferInterrupted
	var iErr *gnoi.InstallError
	if errors.As(err, &iErr) {
		switch iErr.Type {
		case gnoi.InstallErrorIncompatible,
			gnoi.InstallErrorTooLarge,
			gnoi.InstallErrorParseFail,
			gnoi.InstallErrorIntegrityFail,
			gnoi.InstallErrorInstallRunPackage,
			gnoi.InstallErrorNotSupportedBackup:
			// Hard failure — no retry.
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, string(iErr.Type), iErr.Error(), now)
		}
	}
	// Soft failure → TransferInterrupted, honour ResumePolicy.
	if up.Spec.ResumePolicy == "Abort" {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "TransferAborted", err.Error(), now)
	}
	maxRetries := up.Spec.MaxRetries
	if maxRetries == 0 {
		maxRetries = 3
	}
	if up.Status.RetryCount >= maxRetries {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "TransferMaxRetries",
			fmt.Sprintf("transfer interrupted %d time(s): %s", up.Status.RetryCount, err.Error()), now)
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseTransferInterrupted
		cur.Status.RetryCount++
		cur.Status.Message = err.Error()
		cur.Status.FailureReason = "TransferInterrupted"
		r.setReady(cur, metav1.ConditionFalse, "TransferInterrupted", err.Error(), now)
	}, reconcile.Result{RequeueAfter: backoff(up.Status.RetryCount)})
}

func (r *Reconciler) runTransferInterrupted(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	return r.advance(ctx, up, opsv1alpha1.UpgradePhaseTransferring, "RetryTransfer",
		fmt.Sprintf("retrying transfer (attempt %d)", up.Status.RetryCount+1))
}

func (r *Reconciler) runActivating(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	client, err := r.GNOI.GNOIClient(ctx)
	if err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "GNOIClient", err.Error(), now)
	}
	noReboot := up.Spec.Strategy == opsv1alpha1.UpgradeStrategyNoReboot
	activateVersion := up.Status.ValidatedVersion
	if activateVersion == "" {
		activateVersion = up.Spec.TargetVersion
	}
	if stagedVersion, err := r.resolveStagedVersion(ctx, up.Spec.TargetVersion); err == nil && stagedVersion != "" && stagedVersion != activateVersion {
		activateVersion = stagedVersion
		_, _ = r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.ValidatedVersion = stagedVersion
			cur.Status.Message = fmt.Sprintf("device staged %s, activating", stagedVersion)
			r.setCondition(cur, conditionTypeValidated, metav1.ConditionTrue, "StagedVersionResolved",
				fmt.Sprintf("device staged %s for activation", stagedVersion), now)
			r.setReady(cur, metav1.ConditionFalse, "StagedVersionResolved", cur.Status.Message, now)
		}, reconcile.Result{})
	}
	activationMessage := fmt.Sprintf("submitting gNOI OS.Activate for version %s", activateVersion)
	if noReboot {
		activationMessage += " with NoReboot=true"
	} else {
		activationMessage += "; reload expected"
	}
	if _, err := r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		cur.Status.Message = activationMessage
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeActivated, metav1.ConditionFalse, "ActivationRequested", activationMessage, now)
		r.setReady(cur, metav1.ConditionFalse, "ActivationRequested", activationMessage, now)
	}, reconcile.Result{}); err != nil {
		return reconcile.Result{}, err
	}
	r.emitEvent(up, corev1.EventTypeNormal, "ActivationRequested", activationMessage)
	if err := client.Activate(ctx, gnoi.ActivateOpts{Version: activateVersion, NoReboot: noReboot}); err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ActivateFailed", err.Error(), now)
	}
	if noReboot {
		message := "activate complete; device not rebooted (strategy=NoReboot)"
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Phase = opsv1alpha1.UpgradePhaseSucceeded
			cur.Status.FailureReason = ""
			cur.Status.Message = message
			cur.Status.CompletionTime = &metav1.Time{Time: now}
			r.setCondition(cur, conditionTypeActivated, metav1.ConditionTrue, "Activated", message, now)
			r.setReady(cur, metav1.ConditionTrue, "Activated", message, now)
		}, reconcile.Result{})
	}
	timeout := up.Spec.RebootTimeoutSeconds
	if timeout == 0 {
		timeout = 1800
	}
	message := fmt.Sprintf("activate accepted for version %s; waiting up to %ds for reload and target version", activateVersion, timeout)
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeActivated, metav1.ConditionTrue, "Activated", message, now)
		r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionFalse, "ReloadExpected",
			"waiting for device to reload and return on the target version", now)
		r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "VerifyPending",
			"waiting for post-activation version verification", now)
		r.setReady(cur, metav1.ConditionFalse, "Rebooting", message, now)
	}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
}

func (r *Reconciler) runAwaitingReachability(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	client, err := r.GNOI.GNOIClient(ctx)
	if err != nil {
		// Conn missing usually means the device is still rebooting; backoff.
		return r.requeueAwaitingReachability(ctx, up, err, now)
	}
	if _, err := client.Time(ctx); err != nil {
		if isUnsupportedSystemService(err) {
			return r.verifyReachableDevice(ctx, up, client, "device gNOI endpoint is reachable; system service unsupported", now)
		}
		return r.requeueAwaitingReachability(ctx, up, err, now)
	}
	return r.verifyReachableDevice(ctx, up, client, "device is reachable", now)
}

func (r *Reconciler) verifyReachableDevice(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	client *gnoi.Client,
	reachableMessage string,
	now time.Time,
) (reconcile.Result, error) {
	res, err := client.Verify(ctx)
	if err != nil {
		return r.requeueAwaitingReachability(ctx, up, fmt.Errorf("%s but OS.Verify is not ready: %w", reachableMessage, err), now)
	}
	if versionMatches(res.Version, up.Spec.TargetVersion) {
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Phase = opsv1alpha1.UpgradePhaseVerifying
			cur.Status.RunningVersion = res.Version
			cur.Status.Message = fmt.Sprintf("%s on target version %s; running final verification", reachableMessage, res.Version)
			cur.Status.FailureReason = ""
			r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionTrue, "DeviceReachable",
				fmt.Sprintf("device is reachable on target version %s", res.Version), now)
			r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "VerifyPending",
				"running final gNOI OS.Verify", now)
			r.setReady(cur, metav1.ConditionFalse, "DeviceReachable", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: time.Second})
	}
	if !upgradeTimedOut(up, now) {
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
			cur.Status.RunningVersion = res.Version
			cur.Status.Message = fmt.Sprintf("%s but still running %s; waiting for reload/activation to settle toward target %s",
				reachableMessage, res.Version, up.Spec.TargetVersion)
			cur.Status.FailureReason = ""
			r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionTrue, "ReachableOldVersion",
				fmt.Sprintf("device is reachable but still reports %s", res.Version), now)
			r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "VersionPending", cur.Status.Message, now)
			r.setReady(cur, metav1.ConditionFalse, "ActivationSettling", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
	}
	timeoutSec := up.Spec.RebootTimeoutSeconds
	if timeoutSec == 0 {
		timeoutSec = 1800
	}
	return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ActivationDidNotConverge",
		fmt.Sprintf("device stayed on %s; target %s was not reached within %ds after activation", res.Version, up.Spec.TargetVersion, timeoutSec), now)
}

func (r *Reconciler) resolveStagedVersion(ctx context.Context, target string) (string, error) {
	if r.TP == nil {
		return "", nil
	}
	tr := r.TP.GetTransport()
	if tr == nil || tr.Capabilities().Kind != transport.KindRESTCONF {
		return "", nil
	}
	raw, err := tr.Fetch(ctx, "/Cisco-IOS-XE-install-oper:install-oper-data/install-location-information")
	if err != nil {
		return "", err
	}
	return stagedVersionFromInstallOper(raw, target), nil
}

func isUnsupportedSystemService(err error) bool {
	var svcErr *gnoi.ErrServiceUnsupported
	if errors.As(err, &svcErr) {
		return svcErr.Service == gnoi.ServiceSystem
	}
	return status.Code(err) == codes.Unimplemented
}

func (r *Reconciler) requeueAwaitingReachability(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, err error, now time.Time) (reconcile.Result, error) {
	timeoutSec := up.Spec.RebootTimeoutSeconds
	if timeoutSec == 0 {
		timeoutSec = 1800
	}
	if upgradeTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseRebootTimeout, "RebootTimeout",
			fmt.Sprintf("device did not become reachable within %ds after activation", timeoutSec), now)
	}
	delay := awaitingReachabilityPoll
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
		cur.Status.Message = fmt.Sprintf("waiting for device after activation: %s", err.Error())
		r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionFalse, "DeviceUnreachable", err.Error(), now)
		r.setReady(cur, metav1.ConditionFalse, "DeviceUnreachable", cur.Status.Message, now)
	}, reconcile.Result{RequeueAfter: delay})
}

func (r *Reconciler) runVerifying(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	client, err := r.GNOI.GNOIClient(ctx)
	if err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "GNOIClient", err.Error(), now)
	}
	res, err := client.Verify(ctx)
	if err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "VerifyFailed", err.Error(), now)
	}
	if !versionMatches(res.Version, up.Spec.TargetVersion) {
		if stagedVersion, err := r.resolveStagedVersion(ctx, up.Spec.TargetVersion); err == nil && stagedVersion != "" && !upgradeTimedOut(up, now) {
			return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				cur.Status.Phase = opsv1alpha1.UpgradePhaseVerifying
				cur.Status.RunningVersion = res.Version
				cur.Status.ValidatedVersion = stagedVersion
				cur.Status.Message = fmt.Sprintf("device reports %s while target %s is staged as %s; waiting for activation to settle",
					res.Version, up.Spec.TargetVersion, stagedVersion)
				cur.Status.FailureReason = ""
				r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "VerifyPending", cur.Status.Message, now)
				r.setReady(cur, metav1.ConditionFalse, "VerifyPending", cur.Status.Message, now)
			}, reconcile.Result{RequeueAfter: 30 * time.Second})
		}
		rollback := up.Spec.RollbackOnFailure == nil || *up.Spec.RollbackOnFailure
		if rollback {
			return r.advance(ctx, up, opsv1alpha1.UpgradePhaseRollingBack, "VerifyMismatch",
				fmt.Sprintf("device runs %s but spec targets %s; rolling back", res.Version, up.Spec.TargetVersion))
		}
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Phase = opsv1alpha1.UpgradePhaseFailed
			cur.Status.RunningVersion = res.Version
			cur.Status.FailureReason = "VerifyMismatch"
			cur.Status.CompletionTime = &metav1.Time{Time: now}
			cur.Status.Message = fmt.Sprintf("device runs %s; target was %s", res.Version, up.Spec.TargetVersion)
			r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "VerifyMismatch", cur.Status.Message, now)
			r.setReady(cur, metav1.ConditionFalse, "VerifyMismatch", cur.Status.Message, now)
		}, reconcile.Result{})
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseSucceeded
		cur.Status.RunningVersion = res.Version
		cur.Status.CompletionTime = &metav1.Time{Time: now}
		cur.Status.Message = "upgrade complete"
		markTransferComplete(cur)
		r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionTrue, "DeviceReachable",
			fmt.Sprintf("device is reachable on target version %s", res.Version), now)
		r.setCondition(cur, conditionTypeVerified, metav1.ConditionTrue, "Verified",
			fmt.Sprintf("gNOI OS.Verify reports target version %s", res.Version), now)
		r.setReady(cur, metav1.ConditionTrue, "Succeeded", "upgrade complete", now)
	}, reconcile.Result{})
}

func upgradeTimedOut(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) bool {
	timeoutSec := up.Spec.RebootTimeoutSeconds
	if timeoutSec == 0 {
		timeoutSec = 1800
	}
	since := upgradeWaitStart(up)
	return since != nil && now.Sub(*since) > time.Duration(timeoutSec)*time.Second
}

func upgradeWaitStart(up *opsv1alpha1.IOSXESoftwareUpgrade) *time.Time {
	for _, cond := range up.Status.Conditions {
		if cond.Type == conditionTypeActivated && cond.Status == metav1.ConditionTrue {
			return &cond.LastTransitionTime.Time
		}
	}
	if up.Status.StartTime != nil {
		return &up.Status.StartTime.Time
	}
	return nil
}

func (r *Reconciler) handleDelete(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	// Phase-specific cleanup. Phase C ships best-effort: clear the
	// finalizer regardless of phase, but emit a Warning event for
	// "uncancellable" phases so operators can see what happened.
	switch up.Status.Phase {
	case opsv1alpha1.UpgradePhaseValidating,
		opsv1alpha1.UpgradePhaseActivating,
		opsv1alpha1.UpgradePhaseAwaitingReachability,
		opsv1alpha1.UpgradePhaseRollingBack:
		if r.Recorder != nil {
			r.Recorder.Eventf(up, "Warning", "DeleteDuringInflightUpgrade",
				"deletion requested in phase %s; device-side activate may complete asynchronously", up.Status.Phase)
		}
	}
	if controllerutil.ContainsFinalizer(up, Finalizer) {
		controllerutil.RemoveFinalizer(up, Finalizer)
		if err := r.Client.Update(ctx, up); err != nil {
			return reconcile.Result{}, fmt.Errorf("clear finalizer: %w", err)
		}
	}
	return reconcile.Result{}, nil
}

// --- helpers ---

func (r *Reconciler) advance(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, phase opsv1alpha1.UpgradePhase, reason, message string) (reconcile.Result, error) {
	now := r.now()
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		if cur.Status.StartTime == nil && phase != opsv1alpha1.UpgradePhasePending {
			cur.Status.StartTime = &metav1.Time{Time: now}
		}
		cur.Status.Phase = phase
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setReady(cur, metav1.ConditionFalse, reason, message, now)
	}, reconcile.Result{RequeueAfter: time.Second})
}

func (r *Reconciler) pendingMessage(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, reason, message string, now time.Time, requeue time.Duration) (reconcile.Result, error) {
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhasePending
		cur.Status.Message = message
		r.setReady(cur, metav1.ConditionFalse, reason, message, now)
	}, reconcile.Result{RequeueAfter: requeue})
}

func (r *Reconciler) terminal(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, phase opsv1alpha1.UpgradePhase, reason, message string, now time.Time) (reconcile.Result, error) {
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = phase
		cur.Status.FailureReason = reason
		cur.Status.Message = message
		cur.Status.CompletionTime = &metav1.Time{Time: now}
		condStatus := metav1.ConditionFalse
		if phase == opsv1alpha1.UpgradePhaseSucceeded {
			condStatus = metav1.ConditionTrue
			markTransferComplete(cur)
		}
		r.setReady(cur, condStatus, reason, message, now)
	}, reconcile.Result{})
}

func markTransferComplete(up *opsv1alpha1.IOSXESoftwareUpgrade) {
	if up.Status.TransferProgress == nil || up.Status.TransferProgress.TotalBytes <= 0 {
		return
	}
	up.Status.TransferProgress.BytesTransferred = up.Status.TransferProgress.TotalBytes
	up.Status.TransferProgress.Percent = 100
}

func (r *Reconciler) updateTransferProgress(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, bytesReceived uint64, total int64, now time.Time) {
	// Throttle: write progress at most once per 2s.
	if up.Status.TransferProgress != nil && up.Status.TransferProgress.BytesTransferred > 0 {
		if last := up.Status.TransferProgress.BytesTransferred; bytesReceived-uint64(last) < 64*1024 {
			return
		}
	}
	_, _ = r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		percent := int32(0)
		if total > 0 {
			percent = int32(bytesReceived * 100 / uint64(total))
		}
		cur.Status.TransferProgress = &opsv1alpha1.UpgradeTransferProgress{
			BytesTransferred: int64(bytesReceived),
			TotalBytes:       total,
			Percent:          percent,
		}
		cur.Status.Message = fmt.Sprintf("transferring: %d/%d bytes (%d%%)", bytesReceived, total, percent)
	}, reconcile.Result{})
}

func (r *Reconciler) setReady(up *opsv1alpha1.IOSXESoftwareUpgrade, status metav1.ConditionStatus, reason, message string, now time.Time) {
	r.setCondition(up, conditionTypeReady, status, reason, message, now)
}

func (r *Reconciler) setCondition(up *opsv1alpha1.IOSXESoftwareUpgrade, condType string, status metav1.ConditionStatus, reason, message string, now time.Time) {
	cond := metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Time{Time: now},
		ObservedGeneration: up.Generation,
	}
	conds := up.Status.Conditions
	for i, c := range conds {
		if c.Type == cond.Type {
			if c.Status == cond.Status && c.Reason == cond.Reason && c.Message == cond.Message {
				return
			}
			conds[i] = cond
			up.Status.Conditions = conds
			return
		}
	}
	up.Status.Conditions = append(conds, cond)
}

func (r *Reconciler) updateStatus(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, mutate func(*opsv1alpha1.IOSXESoftwareUpgrade), result reconcile.Result) (reconcile.Result, error) {
	var beforePhase, afterPhase opsv1alpha1.UpgradePhase
	var afterReason, afterMessage string
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXESoftwareUpgrade
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(up), &cur); err != nil {
			return err
		}
		beforePhase = cur.Status.Phase
		mutate(&cur)
		cur.Status.ObservedGeneration = cur.Generation
		afterPhase = cur.Status.Phase
		afterMessage = cur.Status.Message
		afterReason = readyReason(cur.Status.Conditions)
		return r.Client.Status().Update(ctx, &cur)
	})
	if err != nil {
		return result, fmt.Errorf("update upgrade status: %w", err)
	}
	if afterPhase != "" && afterPhase != beforePhase {
		eventType := corev1.EventTypeNormal
		if isTerminalFailurePhase(afterPhase) {
			eventType = corev1.EventTypeWarning
		}
		if afterReason == "" {
			afterReason = string(afterPhase)
		}
		r.emitEvent(up, eventType, afterReason, afterMessage)
	}
	return result, nil
}

func readyReason(conditions []metav1.Condition) string {
	for _, cond := range conditions {
		if cond.Type == conditionTypeReady {
			return cond.Reason
		}
	}
	return ""
}

func isTerminalFailurePhase(phase opsv1alpha1.UpgradePhase) bool {
	switch phase {
	case opsv1alpha1.UpgradePhaseFailed,
		opsv1alpha1.UpgradePhasePreflightFailed,
		opsv1alpha1.UpgradePhaseValidationFailed,
		opsv1alpha1.UpgradePhaseRolledBack,
		opsv1alpha1.UpgradePhaseRebootTimeout,
		opsv1alpha1.UpgradePhaseCancelled:
		return true
	default:
		return false
	}
}

func (r *Reconciler) emitEvent(up *opsv1alpha1.IOSXESoftwareUpgrade, eventType, reason, message string) {
	if r.Recorder == nil {
		return
	}
	if message == "" {
		message = string(up.Status.Phase)
	}
	r.Recorder.Event(up, eventType, reason, message)
}

func validateImageSource(src opsv1alpha1.UpgradeImageSource) error {
	count := 0
	if src.URL != "" {
		count++
	}
	if src.ConfigMapRef != nil {
		count++
	}
	if src.LocalPath != "" {
		count++
	}
	if count == 0 {
		return errors.New("imageSource: one of url, configMapRef, or localPath is required")
	}
	if count > 1 {
		return errors.New("imageSource: only one of url, configMapRef, or localPath may be set")
	}
	if src.URL != "" && src.SHA256 == "" {
		return errors.New("imageSource.url requires imageSource.sha256 for integrity verification")
	}
	return nil
}

type installLocationInfo struct {
	Versions []installVersionInfo `json:"install-version-info"`
}

type installVersionInfo struct {
	Version          string `json:"version"`
	VersionExtension string `json:"version-extension"`
	Current          string `json:"current"`
}

func stagedVersionFromInstallOper(raw []byte, target string) string {
	var root map[string][]installLocationInfo
	if err := json.Unmarshal(raw, &root); err != nil {
		return ""
	}
	var best string
	bestPriority := 0
	for key, locations := range root {
		if !strings.HasSuffix(key, "install-location-information") {
			continue
		}
		for _, loc := range locations {
			for _, version := range loc.Versions {
				if version.Version == "" || !versionMatches(version.Version, target) {
					continue
				}
				priority := installVersionPriority(version.Current)
				if priority > bestPriority {
					best = version.activationVersion()
					bestPriority = priority
				}
			}
		}
	}
	return best
}

func (v installVersionInfo) activationVersion() string {
	if v.VersionExtension == "" || strings.HasSuffix(v.Version, "."+v.VersionExtension) {
		return v.Version
	}
	return v.Version + "." + v.VersionExtension
}

func installVersionPriority(state string) int {
	switch state {
	case "install-version-state-in-progress":
		return 4
	case "install-version-state-installed":
		return 3
	case "install-version-state-provisioned-uncommitted":
		return 2
	case "install-version-state-present":
		return 1
	case "install-version-state-provisioned-committed":
		return 1
	default:
		return 0
	}
}

// versionMatches reports whether a device-reported version matches the
// operator-supplied target. IOS-XE returns versions in several shapes
// (release-format "17.15.01a", short build "26.01.01", full install-
// summary form "26.01.01.0.340", oper-data form "17.18.02.0.4112.NNN");
// the operator may legitimately supply the shortest unambiguous prefix
// rather than copying the exact device string. A device-side version
// matches the target when either:
//   - they are byte-equal, or
//   - the device version begins with target + "." (target is a strict
//     prefix on a dotted-segment boundary, so "26.01.01" matches
//     "26.01.01.0.340" but not "26.01.011" or "26.01.01a").
//
// Empty target is rejected by CRD validation; the function only sees
// non-empty target values from the reconciler.
func versionMatches(deviceVersion, target string) bool {
	if deviceVersion == target {
		return true
	}
	return strings.HasPrefix(deviceVersion, target+".")
}

func backoff(retryCount int32) time.Duration {
	switch {
	case retryCount <= 0:
		return 30 * time.Second
	case retryCount == 1:
		return 60 * time.Second
	case retryCount == 2:
		return 120 * time.Second
	default:
		return 300 * time.Second
	}
}
