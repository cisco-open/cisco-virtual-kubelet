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
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	commonpb "github.com/openconfig/gnoi/types"
	"github.com/virtual-kubelet/virtual-kubelet/log"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelcodes "go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/correlation"
	"github.com/cisco/virtual-kubelet-cisco/internal/telemetry/semconv"
)

// SetupWithManager registers the IOSXESoftwareUpgrade controller.
func (r *Reconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&opsv1alpha1.IOSXESoftwareUpgrade{}).
		Complete(r)
}

// upgradeFinalizer guarantees the deletion path can release a Lease when no
// device mutation was submitted. Once a mutation marker exists, deletion only
// removes the finalizer and leaves the Lease quarantined until its TTL; neither
// gNOI OS nor the native lifecycle backend exposes a reliable cancel RPC.
const upgradeFinalizer = "ops.cisco.vk/iosxesoftwareupgrade-cleanup"

const (
	conditionTypeReady           = "Ready"
	conditionTypeImageResolved   = "ImageResolved"
	conditionTypeStaged          = "Staged"
	conditionTypeTransferred     = "Transferred"
	conditionTypeValidated       = "Validated"
	conditionTypeActivated       = "Activated"
	conditionTypeDeviceReachable = "DeviceReachable"
	conditionTypeVerified        = "Verified"
	conditionTypeRollback        = "Rollback"
	conditionTypeMutationSettled = "DeviceMutationSettled"
)

const (
	awaitingReachabilityPoll = 30 * time.Second
	installInventoryPoll     = 15 * time.Second
	controlRPCTimeout        = 30 * time.Second
	lifecycleMutationTimeout = 90 * time.Second
	activationRPCTimeout     = 90 * time.Second
	mutationResultGrace      = 5 * time.Second
)

// Reconciler advances IOSXESoftwareUpgrade CRs through a durable phase state
// machine. Mutation intents are atomically persisted before dispatch.
type Reconciler struct {
	Client          client.Client
	Reader          client.Reader
	Recorder        record.EventRecorder
	DeviceName      string
	DeviceNamespace string
	GNOI            gnoi.Provider
	Lifecycle       softwarelifecycle.Backend
	ImageResolver   ImageResolver
	MutationLeaser  *engine.FamilyLeaser
	// BeforeMutation prepares device maintenance after the shared Lease is
	// owned. Errors prevent dispatch; implementations must be idempotent.
	BeforeMutation func(context.Context) error

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
func (r *Reconciler) Reconcile(ctx context.Context, req reconcile.Request) (result reconcile.Result, retErr error) {
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
	ctx, _ = correlation.ApplyAnnotations(ctx, up.Annotations, now)
	ctx, span := correlation.Start(
		ctx,
		otel.Tracer("cisco-virtual-kubelet/softwareupgrade-reconciler"),
		"cvk.iosxesoftwareupgrade.reconcile",
		oteltrace.WithSpanKind(oteltrace.SpanKindInternal),
		oteltrace.WithAttributes(
			attribute.String("cisco.vk.device.name", r.DeviceName),
			attribute.String("k8s.namespace.name", up.Namespace),
			attribute.String("k8s.resource.name", up.Name),
			attribute.String("k8s.resource.kind", "IOSXESoftwareUpgrade"),
			attribute.String("cvk.softwareupgrade.phase", string(up.Status.Phase)),
			attribute.String(semconv.CvkEntityType, semconv.EntityTypeOperation),
			attribute.String(semconv.CvkEntityID, softwareUpgradeEntityID(&up)),
			attribute.String(semconv.CvkEvidenceType, semconv.EvidenceTypeOperatorAction),
			attribute.String(semconv.CvkWorkflowName, "software.lifecycle"),
		),
	)
	setSoftwareUpgradeSpanOutcome(span, up.Status.Phase, up.Status.FailureReason, up.Status.Message)
	defer func() {
		if retErr != nil {
			span.RecordError(retErr)
			span.SetStatus(otelcodes.Error, "reconcile")
		}
		span.End()
	}()

	// Deletion path.
	if !up.DeletionTimestamp.IsZero() {
		return r.handleDelete(ctx, &up, now)
	}
	unsupportedModel := unsupportedExecutionModel(&up)
	terminalLegacyRisk := !unsupportedModel && terminalUpgradePhase(up.Status.Phase) &&
		mutationguard.UpgradeRequiresQuarantineAt(&up, now)
	if !unsupportedModel && terminalUpgradePhase(up.Status.Phase) && !terminalLegacyRisk {
		if !retainMutationLeaseUntilExpiry(&up) {
			if err := r.releaseMutationLease(ctx, &up); err != nil {
				return reconcile.Result{}, err
			}
		}
		return reconcile.Result{}, nil
	}
	// Install the finalizer before this controller can acquire a device Lease.
	// Otherwise deletion in Resolving can remove the CR before the cleanup path
	// has an opportunity to release an as-yet unused Lease.
	if !controllerutil.ContainsFinalizer(&up, upgradeFinalizer) {
		controllerutil.AddFinalizer(&up, upgradeFinalizer)
		if err := r.Client.Update(ctx, &up); err != nil {
			return reconcile.Result{}, fmt.Errorf("add software-upgrade finalizer: %w", err)
		}
		return reconcile.Result{RequeueAfter: time.Second}, nil
	}
	if unsupportedModel {
		return r.holdUnsupportedExecutionModel(ctx, &up, now)
	}
	if terminalLegacyRisk {
		return r.holdLegacyTerminalQuarantine(ctx, &up, now)
	}
	if handled, result, err := r.ensureExecutionModel(ctx, &up, now); handled {
		return result, err
	}
	if up.Status.Phase != "" && up.Status.Phase != opsv1alpha1.UpgradePhasePending {
		owned, result, err := r.ensureMutationLease(ctx, &up, now)
		if err != nil || !owned {
			return result, err
		}
	}

	switch up.Status.Phase {
	case "", opsv1alpha1.UpgradePhasePending:
		return r.runPending(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseResolving:
		return r.runResolving(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseStaging:
		return r.runStaging(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseTransferring:
		return r.runTransferring(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseTransferInterrupted:
		return r.runTransferInterrupted(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseValidating:
		return r.runValidating(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseActivating:
		return r.runActivating(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseAwaitingReachability:
		return r.runAwaitingReachability(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseVerifying:
		return r.runVerifying(ctx, &up, now)
	case opsv1alpha1.UpgradePhaseRollingBack:
		return r.runRollingBack(ctx, &up, now)
	default:
		// Terminal phases: nothing to do.
		return reconcile.Result{}, nil
	}
}

func softwareUpgradeEntityID(up *opsv1alpha1.IOSXESoftwareUpgrade) string {
	if up == nil {
		return ""
	}
	if up.UID != "" {
		return string(up.UID)
	}
	if up.Namespace != "" {
		return up.Namespace + "/" + up.Name
	}
	return up.Name
}

func setSoftwareUpgradeSpanOutcome(span oteltrace.Span, phase opsv1alpha1.UpgradePhase, reason, message string) {
	if phase == "" {
		return
	}
	span.SetAttributes(
		attribute.String("cvk.softwareupgrade.phase", string(phase)),
		attribute.String("cvk.softwareupgrade.reason", reason),
	)
	if isTerminalFailurePhase(phase) {
		description := reason
		if description == "" {
			description = message
		}
		if description == "" {
			description = string(phase)
		}
		span.SetStatus(otelcodes.Error, description)
	} else if phase == opsv1alpha1.UpgradePhaseSucceeded || phase == opsv1alpha1.UpgradePhaseStagedForNextBoot {
		span.SetStatus(otelcodes.Ok, "")
	}
}

// --- per-phase handlers ---

func (r *Reconciler) runPending(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	if up.Spec.MaintenanceWindow != nil {
		w := up.Spec.MaintenanceWindow
		if w.NotBefore != nil && w.NotAfter != nil && w.NotBefore.After(w.NotAfter.Time) {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "InvalidMaintenanceWindow",
				"maintenance window NotBefore must not be later than NotAfter", now)
		}
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

	// Admission normally enforces these invariants. Keep runtime validation
	// for objects created before the CRD was upgraded and for unit-test/fake
	// clients that do not execute CEL.
	if err := validateImageSource(up.Spec.ImageSource); err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "InvalidImageSource", err.Error(), now)
	}
	if err := softwarelifecycle.ValidateTargetVersion(up.Spec.TargetVersion); err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "InvalidTargetVersion", err.Error(), now)
	}
	if up.Spec.Strategy == opsv1alpha1.UpgradeStrategyISSU {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "ISSUVerificationUnsupported",
			"strategy ISSU is not available until the platform lifecycle backend can verify that IOS XE selected the ISSU activation path", now)
	}
	owner, err := r.deviceUpgradeOwner(ctx, up, now)
	if err != nil {
		return reconcile.Result{}, err
	}
	if owner != "" && owner != up.Name {
		return r.pendingMessage(ctx, up, "DeviceUpgradeLocked",
			fmt.Sprintf("waiting for IOSXESoftwareUpgrade %s to release device %s", owner, up.Spec.DeviceRef.Name),
			now, installInventoryPoll)
	}
	if r.GNOI == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "NoGNOIProvider",
			"gNOI provider is required for IOS XE software lifecycle operations", now)
	}
	if imageSourceNeedsLifecycle(up.Spec.ImageSource) && r.Lifecycle == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "SoftwareLifecycleUnsupported",
			"the selected image source requires a platform software lifecycle backend", now)
	}
	owned, result, err := r.ensureMutationLease(ctx, up, now)
	if err != nil || !owned {
		return result, err
	}

	return r.advance(ctx, up, opsv1alpha1.UpgradePhaseResolving, "Resolving", "preflight passed, resolving image")
}

func (r *Reconciler) runResolving(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	if r.GNOI == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "NoGNOIProvider",
			"gNOI provider is required for IOS XE software lifecycle operations", now)
	}
	gnoiClient, err := r.gnoiClient(ctx)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		return r.handlePreflightGNOIError(ctx, up, "gNOI client", err, now)
	}
	verify, err := verifyOS(ctx, gnoiClient)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		return r.handlePreflightGNOIError(ctx, up, "gNOI OS.Verify", err, now)
	}
	requiresIndividual := up.Status.IndividualSupervisorInstall || verify.IndividualSupervisorInstall
	if versionMatches(verify.Version, up.Spec.TargetVersion) {
		if ready, detail := allRequiredSupervisorsRunTarget(verify, up.Spec.TargetVersion, requiresIndividual); !ready {
			if verify.Standby.State == gnoi.StandbyStateUnavailable {
				return r.waitForSupervisorTarget(ctx, up, opsv1alpha1.UpgradePhaseResolving, detail, now)
			}
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "SupervisorTargetMismatch", detail, now)
		}
		return r.updateTerminalStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			if cur.Status.StartTime == nil {
				cur.Status.StartTime = &metav1.Time{Time: now}
			}
			cur.Status.Phase = opsv1alpha1.UpgradePhaseSucceeded
			cur.Status.RunningVersion = verify.Version
			cur.Status.IndividualSupervisorInstall = cur.Status.IndividualSupervisorInstall || requiresIndividual
			cur.Status.Message = fmt.Sprintf("target version %s already running; lifecycle mutation skipped", verify.Version)
			cur.Status.FailureReason = ""
			cur.Status.CompletionTime = &metav1.Time{Time: now}
			r.setCondition(cur, conditionTypeImageResolved, metav1.ConditionTrue, "AlreadyRunning",
				"image resolution skipped because the device is already on the target version", now)
			r.setCondition(cur, conditionTypeTransferred, metav1.ConditionTrue, "AlreadyRunning",
				"image transfer skipped because the device is already on the target version", now)
			r.setCondition(cur, conditionTypeActivated, metav1.ConditionTrue, "AlreadyRunning",
				"activation skipped because the device is already on the target version", now)
			r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionTrue, "AlreadyRunning",
				fmt.Sprintf("device is reachable on target version %s", verify.Version), now)
			r.setCondition(cur, conditionTypeVerified, metav1.ConditionTrue, "AlreadyRunning",
				fmt.Sprintf("gNOI OS.Verify reports target version %s", verify.Version), now)
			r.setReady(cur, metav1.ConditionTrue, "AlreadyRunning", cur.Status.Message, now)
		}, reconcile.Result{})
	}
	if up.Spec.Strategy == opsv1alpha1.UpgradeStrategyNoReboot && requiresIndividual {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "IndividualSupervisorNoRebootUnsupported",
			"NoReboot cannot safely prove the required per-supervisor boot sequence; use strategy Reload", now)
	}

	previousVersion := up.Status.PreviousVersion
	if previousVersion == "" {
		previousVersion = verify.Version
	}
	if imageSourceStreamsBytes(up.Spec.ImageSource) {
		if r.ImageResolver == nil {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "NoImageResolver",
				"image resolver not configured on reconciler", now)
		}
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			if cur.Status.StartTime == nil {
				cur.Status.StartTime = &metav1.Time{Time: now}
			}
			cur.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
			rememberPreviousVersion(cur, previousVersion)
			cur.Status.IndividualSupervisorInstall = cur.Status.IndividualSupervisorInstall || requiresIndividual
			cur.Status.Message = "preflight passed; resolving a content-addressed image for gNOI OS.Install"
			cur.Status.FailureReason = ""
			r.setCondition(cur, conditionTypeTransferred, metav1.ConditionFalse, "TransferPending",
				"waiting to resolve and stream image bytes with gNOI OS.Install", now)
			r.setReady(cur, metav1.ConditionFalse, "TransferPending", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: time.Second})
	}

	path, digest := deviceFileIntegrity(up.Spec.ImageSource)
	if path != "" && (up.Spec.ImageSource.DeviceFile != nil || digest != "") {
		if r.Lifecycle == nil {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "SoftwareLifecycleUnsupported",
				"device-file path validation requires a platform software lifecycle backend", now)
		}
		if err := r.Lifecycle.ValidateDeviceFilePath(path); err != nil {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "InvalidDevicePath", err.Error(), now)
		}
	}
	expectedDigest := ""
	if digest != "" {
		expectedDigest = "sha256:" + digest
	}
	if up.Status.SourceDigest != "" && up.Status.SourceDigest != expectedDigest {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "SourceDigestMismatch",
			fmt.Sprintf("persisted source digest %s does not match the immutable image source digest %s", up.Status.SourceDigest, expectedDigest), now)
	}
	if expectedDigest != "" && up.Status.SourceDigest == "" {
		now, err = r.verifyDeviceFileDigestWithinInstallDeadline(ctx, up, gnoiClient, path, digest, now)
		if errors.Is(err, errInstallDeadlineExceeded) {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallTimeout",
				fmt.Sprintf("device-file verification did not complete within %s", installTimeout(up)), now)
		}
		if err != nil {
			if ctx.Err() != nil {
				return reconcile.Result{}, err
			}
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "DeviceFileHashFailed", err.Error(), now)
		}
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			if cur.Status.StartTime == nil {
				cur.Status.StartTime = &metav1.Time{Time: now}
			}
			rememberPreviousVersion(cur, previousVersion)
			cur.Status.SourceDigest = expectedDigest
			cur.Status.IndividualSupervisorInstall = cur.Status.IndividualSupervisorInstall || requiresIndividual
			cur.Status.Message = fmt.Sprintf("verified device file %s; inspecting install inventory", path)
			cur.Status.FailureReason = ""
			r.setCondition(cur, conditionTypeImageResolved, metav1.ConditionTrue, "DeviceFileVerified", cur.Status.Message, now)
			r.setReady(cur, metav1.ConditionFalse, "InventoryPending", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: time.Second})
	}
	if up.Spec.ImageSource.DeviceFile != nil && stagingRequestSubmitted(up) {
		return r.advanceStagingValidation(ctx, up, "StagingObservationPending",
			fmt.Sprintf("observing durably recorded staging operation %s before trusting device inventory", up.Status.StagingOperationID), now)
	}

	image, inspectErr := r.inspectTarget(ctx, up.Spec.TargetVersion)
	if inspectErr != nil {
		switch {
		case errors.Is(inspectErr, softwarelifecycle.ErrTargetNotFound):
			if up.Spec.ImageSource.DeviceFile == nil {
				return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "TargetNotInstalled",
					fmt.Sprintf("target version %s is not present in the device install inventory", up.Spec.TargetVersion), now)
			}
			operationID := up.Status.StagingOperationID
			if operationID == "" {
				operationID = uuid.NewString()
			}
			return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				if cur.Status.StartTime == nil {
					cur.Status.StartTime = &metav1.Time{Time: now}
				}
				cur.Status.Phase = opsv1alpha1.UpgradePhaseStaging
				rememberPreviousVersion(cur, previousVersion)
				cur.Status.StagingOperationID = operationID
				cur.Status.InventoryState = opsv1alpha1.UpgradeInventoryStateAbsent
				cur.Status.IndividualSupervisorInstall = cur.Status.IndividualSupervisorInstall || requiresIndividual
				cur.Status.Message = fmt.Sprintf("target is absent from install inventory; staging device file %s", path)
				cur.Status.FailureReason = ""
				r.setCondition(cur, conditionTypeStaged, metav1.ConditionFalse, "StagingPending", cur.Status.Message, now)
				r.setReady(cur, metav1.ConditionFalse, "StagingPending", cur.Status.Message, now)
			}, reconcile.Result{RequeueAfter: time.Second})
		case errors.Is(inspectErr, softwarelifecycle.ErrAmbiguousTarget):
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "AmbiguousTargetVersion", inspectErr.Error(), now)
		case errors.Is(inspectErr, softwarelifecycle.ErrUnsupported):
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "SoftwareLifecycleUnsupported", inspectErr.Error(), now)
		default:
			if installTimedOut(up, now) {
				return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "InventoryTimeout", inspectErr.Error(), now)
			}
			return r.waitForInventory(ctx, up, "InventoryUnavailable", inspectErr.Error(), now)
		}
	}
	if up.Spec.ImageSource.DeviceFile != nil &&
		(image.State.Activatable() || image.State == softwarelifecycle.InventoryStateInProgress) {
		return r.rejectUncorrelatedDeviceFileInventory(ctx, up, image, now)
	}
	if !image.State.Activatable() {
		if image.State == softwarelifecycle.InventoryStateInProgress && !installTimedOut(up, now) {
			return r.waitForInventory(ctx, up, "InstallInProgress",
				fmt.Sprintf("target version %s is still being registered", image.Version), now)
		}
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, "TargetNotActivatable",
			fmt.Sprintf("target version %s has non-activatable inventory state %s", image.Version, image.State), now)
	}
	return r.targetReadyForActivation(ctx, up, image, previousVersion, requiresIndividual, now, "PreinstalledVersionResolved")
}

func (r *Reconciler) runStaging(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	if up.Spec.ImageSource.DeviceFile == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "InvalidStagingState",
			"Staging phase requires imageSource.deviceFile", now)
	}
	if r.Lifecycle == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "SoftwareLifecycleUnsupported",
			"device-file staging requires a platform software lifecycle backend", now)
	}
	if up.Status.SourceDigest != "sha256:"+up.Spec.ImageSource.DeviceFile.SHA256 {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "DeviceFileNotVerified",
			"device-file digest was not durably verified before staging", now)
	}
	if stagingRequestSubmitted(up) {
		if remaining := stagingRPCGraceRemaining(up, now); remaining > 0 {
			return reconcile.Result{RequeueAfter: boundedDuration(time.Second, remaining)}, nil
		}
		return r.advanceStagingValidation(ctx, up, "StagingObservationPending",
			fmt.Sprintf("observing durably recorded staging operation %s before trusting device inventory", up.Status.StagingOperationID), now)
	}

	image, err := r.inspectTarget(ctx, up.Spec.TargetVersion)
	if err == nil {
		if image.State.Activatable() || image.State == softwarelifecycle.InventoryStateInProgress {
			return r.rejectUncorrelatedDeviceFileInventory(ctx, up, image, now)
		}
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "TargetNotActivatable",
			fmt.Sprintf("target version %s has non-activatable inventory state %s", image.Version, image.State), now)
	}
	if errors.Is(err, softwarelifecycle.ErrAmbiguousTarget) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "AmbiguousTargetVersion", err.Error(), now)
	}
	if !errors.Is(err, softwarelifecycle.ErrTargetNotFound) {
		if errors.Is(err, softwarelifecycle.ErrUnsupported) {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "SoftwareLifecycleUnsupported", err.Error(), now)
		}
		if installTimedOut(up, now) {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InventoryTimeout", err.Error(), now)
		}
		return r.waitForInventory(ctx, up, "InventoryUnavailable", err.Error(), now)
	}

	if maintenanceWindowExpired(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "MaintenanceWindowExpired",
			"maintenance window closed before device-file staging could be submitted", now)
	}
	if err := r.Lifecycle.ValidateDeviceFilePath(up.Spec.ImageSource.DeviceFile.Path); err != nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InvalidDevicePath", err.Error(), now)
	}
	if r.GNOI == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "NoGNOIProvider",
			"gNOI provider is required to re-verify the device file before staging", now)
	}
	gnoiClient, err := r.gnoiClient(ctx)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		return r.handlePreflightGNOIError(ctx, up, "gNOI client for device-file re-verification", err, now)
	}
	now, err = r.verifyDeviceFileDigestWithinInstallDeadline(ctx, up, gnoiClient,
		up.Spec.ImageSource.DeviceFile.Path, up.Spec.ImageSource.DeviceFile.SHA256, now)
	if errors.Is(err, errInstallDeadlineExceeded) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallTimeout",
			fmt.Sprintf("device-file re-verification did not complete within %s", installTimeout(up)), now)
	}
	if err != nil {
		if ctx.Err() != nil {
			return reconcile.Result{}, err
		}
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "DeviceFileChanged", err.Error(), now)
	}
	// File.Get can stream a large device image. The helper refreshes the clock;
	// recheck every time-dependent guard and the shared Lease immediately before
	// claiming the irreversible native install request.
	if maintenanceWindowExpired(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "MaintenanceWindowExpired",
			"maintenance window closed while re-verifying the device file", now)
	}
	if installTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallTimeout",
			fmt.Sprintf("device-file staging did not begin within %s", installTimeout(up)), now)
	}
	owned, result, err := r.ensureMutationLease(ctx, up, now)
	if err != nil || !owned {
		return result, err
	}

	message := fmt.Sprintf("submitting IOS XE device-file staging operation %s", up.Status.StagingOperationID)
	claimed, deleting, err := r.claimStaging(ctx, up, message, now)
	if err != nil {
		return reconcile.Result{}, err
	}
	if deleting != nil {
		return r.handleDelete(ctx, deleting, now)
	}
	if !claimed {
		return reconcile.Result{RequeueAfter: time.Second}, nil
	}
	r.emitEvent(up, corev1.EventTypeNormal, "StagingRequested", message)

	registerCtx, cancelRegister := context.WithTimeout(ctx,
		boundedDuration(lifecycleMutationTimeout, remainingInstallTime(up, now)))
	registration, registerErr := r.registerDeviceFile(registerCtx, softwarelifecycle.DeviceFileRequest{
		Path:        up.Spec.ImageSource.DeviceFile.Path,
		OperationID: up.Status.StagingOperationID,
	})
	cancelRegister()
	now = r.now()
	if registerErr != nil {
		if errors.Is(registerErr, softwarelifecycle.ErrUnsupported) ||
			errors.Is(registerErr, softwarelifecycle.ErrInvalidDevicePath) ||
			errors.Is(registerErr, softwarelifecycle.ErrInvalidOperationID) {
			return r.terminalAfterMutation(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "StagingRejected", registerErr.Error(), now)
		}
		return r.advanceStagingValidation(ctx, up, "StagingResponseLost",
			fmt.Sprintf("staging response was indeterminate; observing operation %s without replay: %s", up.Status.StagingOperationID, registerErr), now)
	}
	if registration.OperationID != up.Status.StagingOperationID {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "StagingOperationMismatch",
			fmt.Sprintf("device acknowledged staging operation %q, want %q", registration.OperationID, up.Status.StagingOperationID), now)
	}
	return r.advanceStagingValidation(ctx, up, "StagingAccepted",
		fmt.Sprintf("device accepted staging operation %s; waiting for install inventory", registration.OperationID), now)
}

func (r *Reconciler) runValidating(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	if r.Lifecycle == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "SoftwareLifecycleUnsupported",
			"install inventory validation requires a platform software lifecycle backend", now)
	}
	if installTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallTimeout",
			fmt.Sprintf("target %s did not become activatable within %s", up.Spec.TargetVersion, installTimeout(up)), now)
	}

	if up.Status.StagingOperationID == "" {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "StagingOperationMissing",
			"native install validation requires a durable device-file staging operation ID", now)
	}

	observation, err := r.observeDeviceFile(ctx, up.Status.StagingOperationID, up.Spec.TargetVersion)
	if err != nil {
		if errors.Is(err, softwarelifecycle.ErrAmbiguousTarget) {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "AmbiguousTargetVersion", err.Error(), now)
		}
		if errors.Is(err, softwarelifecycle.ErrUnsupported) {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "SoftwareLifecycleUnsupported", err.Error(), now)
		}
		return r.waitForValidation(ctx, up, "StagingObservationPending", err.Error(), now)
	}
	if observation.OperationID != up.Status.StagingOperationID {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "StagingOperationMismatch",
			fmt.Sprintf("device reported staging operation %q while observing %q", observation.OperationID, up.Status.StagingOperationID), now)
	}
	if observation.State == softwarelifecycle.OperationStateFailed {
		return r.terminalAfterMutation(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "StagingFailed",
			fmt.Sprintf("device staging operation %s failed", observation.OperationID), now)
	}
	if observation.State != softwarelifecycle.OperationStateSucceeded ||
		observation.Image == nil || !observation.Image.State.Activatable() {
		message := fmt.Sprintf("staging operation %s is %s; waiting for an activatable inventory version",
			observation.OperationID, observation.State)
		if observation.Image != nil && observation.Image.State == softwarelifecycle.InventoryStateInvalid {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InvalidInventoryState", message, now)
		}
		return r.waitForValidation(ctx, up, "StagingInProgress", message, now)
	}
	return r.targetReadyForActivation(ctx, up, *observation.Image, up.Status.PreviousVersion,
		up.Status.IndividualSupervisorInstall, now, "DeviceFileStaged")
}

func (r *Reconciler) inspectTarget(ctx context.Context, target string) (softwarelifecycle.InventoryImage, error) {
	if r.Lifecycle == nil {
		return softwarelifecycle.InventoryImage{}, softwarelifecycle.ErrUnsupported
	}
	callCtx, cancel := context.WithTimeout(ctx, controlRPCTimeout)
	defer cancel()
	return r.Lifecycle.Inspect(callCtx, target)
}

func (r *Reconciler) registerDeviceFile(ctx context.Context, request softwarelifecycle.DeviceFileRequest) (softwarelifecycle.DeviceFileRegistration, error) {
	if r.Lifecycle == nil {
		return softwarelifecycle.DeviceFileRegistration{}, softwarelifecycle.ErrUnsupported
	}
	return r.Lifecycle.RegisterDeviceFile(ctx, request)
}

func (r *Reconciler) observeDeviceFile(ctx context.Context, operationID, target string) (softwarelifecycle.DeviceFileObservation, error) {
	if r.Lifecycle == nil {
		return softwarelifecycle.DeviceFileObservation{}, softwarelifecycle.ErrUnsupported
	}
	callCtx, cancel := context.WithTimeout(ctx, controlRPCTimeout)
	defer cancel()
	return r.Lifecycle.ObserveDeviceFile(callCtx, operationID, target)
}

func (r *Reconciler) rejectUncorrelatedDeviceFileInventory(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	image softwarelifecycle.InventoryImage,
	now time.Time,
) (reconcile.Result, error) {
	return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "UncorrelatedDeviceFileInventory",
		fmt.Sprintf("target version %s was already %s before this upgrade durably recorded its device-file staging request; refusing to attribute or activate uncorrelated inventory",
			image.Version, image.State), now)
}

func (r *Reconciler) targetReadyForActivation(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	image softwarelifecycle.InventoryImage,
	previousVersion string,
	requiresIndividual bool,
	now time.Time,
	reason string,
) (reconcile.Result, error) {
	if strings.TrimSpace(image.Version) == "" || !versionMatches(image.Version, up.Spec.TargetVersion) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InventoryVersionMismatch",
			fmt.Sprintf("inventory returned version %q for target %q", image.Version, up.Spec.TargetVersion), now)
	}
	if !image.State.Activatable() {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "TargetNotActivatable",
			fmt.Sprintf("inventory version %s has state %s", image.Version, image.State), now)
	}
	boundSourcePath := ""
	if source := up.Spec.ImageSource.DeviceFile; source != nil {
		boundSourcePath = source.Path
	} else if up.Spec.ImageSource.LocalPathSHA256 != "" {
		boundSourcePath = up.Spec.ImageSource.LocalPath
	}
	if boundSourcePath != "" {
		if strings.TrimSpace(image.SourcePath) == "" {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InventorySourceUnknown",
				fmt.Sprintf("inventory version %s does not report a consistent source path; cannot bind it to verified device file %q", image.Version, boundSourcePath), now)
		}
		if canonicalDevicePath(boundSourcePath) != canonicalDevicePath(image.SourcePath) {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InventorySourceMismatch",
				fmt.Sprintf("inventory version %s came from %q, not verified device file %q", image.Version, image.SourcePath, boundSourcePath), now)
		}
	}
	message := fmt.Sprintf("device install inventory resolved exact activatable version %s", image.Version)
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		if cur.Status.StartTime == nil {
			cur.Status.StartTime = &metav1.Time{Time: now}
		}
		cur.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		rememberPreviousVersion(cur, previousVersion)
		cur.Status.ValidatedVersion = image.Version
		cur.Status.IndividualSupervisorInstall = cur.Status.IndividualSupervisorInstall || requiresIndividual
		cur.Status.InventoryState = upgradeInventoryState(image.State)
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeMutationSettled, metav1.ConditionTrue, "InstallCompleted", message, now)
		r.setCondition(cur, conditionTypeStaged, metav1.ConditionTrue, reason, message, now)
		r.setCondition(cur, conditionTypeValidated, metav1.ConditionTrue, reason, message, now)
		r.setCondition(cur, conditionTypeTransferred, metav1.ConditionTrue, "TransferNotRequired",
			"activation uses a version already present in the native install inventory", now)
		r.setCondition(cur, conditionTypeActivated, metav1.ConditionFalse, "ActivationPending",
			"waiting to submit gNOI OS.Activate", now)
		r.setReady(cur, metav1.ConditionFalse, reason, message, now)
	}, reconcile.Result{RequeueAfter: time.Second})
}

func (r *Reconciler) advanceStagingValidation(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, reason, message string, now time.Time) (reconcile.Result, error) {
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseValidating
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeStaged, metav1.ConditionFalse, reason, message, now)
		r.setCondition(cur, conditionTypeValidated, metav1.ConditionFalse, reason, message, now)
		r.setReady(cur, metav1.ConditionFalse, reason, message, now)
	}, reconcile.Result{RequeueAfter: installInventoryPoll})
}

func (r *Reconciler) waitForInventory(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, reason, message string, now time.Time) (reconcile.Result, error) {
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		if cur.Status.StartTime == nil {
			cur.Status.StartTime = &metav1.Time{Time: now}
		}
		cur.Status.Message = "waiting for device install inventory: " + message
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeValidated, metav1.ConditionFalse, reason, cur.Status.Message, now)
		r.setReady(cur, metav1.ConditionFalse, reason, cur.Status.Message, now)
	}, reconcile.Result{RequeueAfter: installInventoryPoll})
}

func (r *Reconciler) waitForValidation(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, reason, message string, now time.Time) (reconcile.Result, error) {
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseValidating
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeValidated, metav1.ConditionFalse, reason, message, now)
		r.setReady(cur, metav1.ConditionFalse, reason, message, now)
	}, reconcile.Result{RequeueAfter: installInventoryPoll})
}

func (r *Reconciler) waitForSupervisorTarget(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	phase opsv1alpha1.UpgradePhase,
	message string,
	now time.Time,
) (reconcile.Result, error) {
	if installTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "SupervisorTargetTimeout", message, now)
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		if cur.Status.StartTime == nil {
			cur.Status.StartTime = &metav1.Time{Time: now}
		}
		cur.Status.Phase = phase
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setReady(cur, metav1.ConditionFalse, "StandbyUnavailable", message, now)
	}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
}

func (r *Reconciler) handlePreflightGNOIError(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, operation string, err error, now time.Time) (reconcile.Result, error) {
	if reason, permanent := permanentGNOIError(err); permanent {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhasePreflightFailed, reason,
			fmt.Sprintf("%s preflight failed: %s", operation, err), now)
	}
	if installTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallTimeout",
			fmt.Sprintf("%s did not become ready within %s: %s", operation, installTimeout(up), err), now)
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		if cur.Status.StartTime == nil {
			cur.Status.StartTime = &metav1.Time{Time: now}
		}
		cur.Status.Phase = opsv1alpha1.UpgradePhaseResolving
		cur.Status.Message = fmt.Sprintf("waiting for %s before image resolution: %s", operation, err)
		cur.Status.FailureReason = ""
		r.setReady(cur, metav1.ConditionFalse, "VerifyPending", cur.Status.Message, now)
	}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
}

var errInstallDeadlineExceeded = errors.New("install deadline exceeded")

func (r *Reconciler) verifyDeviceFileDigestWithinInstallDeadline(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	client *gnoi.Client,
	path string,
	expected string,
	now time.Time,
) (time.Time, error) {
	if installTimedOut(up, now) {
		return now, errInstallDeadlineExceeded
	}
	getCtx, cancel := context.WithTimeout(ctx, remainingInstallTime(up, now))
	err := r.verifyDeviceFileDigest(getCtx, client, path, expected)
	deadlineExceeded := errors.Is(getCtx.Err(), context.DeadlineExceeded)
	cancel()
	now = r.now()
	if ctx.Err() != nil {
		return now, ctx.Err()
	}
	if deadlineExceeded || installTimedOut(up, now) {
		return now, errInstallDeadlineExceeded
	}
	return now, err
}

func (r *Reconciler) verifyDeviceFileDigest(ctx context.Context, client *gnoi.Client, path, expected string) error {
	if client == nil {
		return errors.New("gNOI client is nil")
	}
	contentHash := sha256.New()
	serverHash, err := client.Get(ctx, path, contentHash)
	if err != nil {
		return fmt.Errorf("read and hash device file %s with gNOI File.Get: %w", path, err)
	}
	if serverHash.GetMethod() != commonpb.HashType_SHA256 {
		return fmt.Errorf("device returned hash method %s for %s, want SHA256", serverHash.GetMethod().String(), path)
	}
	got := hex.EncodeToString(contentHash.Sum(nil))
	if got != expected {
		return fmt.Errorf("SHA256 mismatch for %s: got %s want %s", path, got, expected)
	}
	if server := hex.EncodeToString(serverHash.GetHash()); server != got {
		return fmt.Errorf("device-reported SHA256 mismatch for %s: device reported %s but streamed bytes hashed to %s", path, server, got)
	}
	return nil
}

func deviceFileIntegrity(source opsv1alpha1.UpgradeImageSource) (path, digest string) {
	if source.DeviceFile != nil {
		return source.DeviceFile.Path, source.DeviceFile.SHA256
	}
	return source.LocalPath, source.LocalPathSHA256
}

func canonicalDevicePath(path string) string {
	parts := strings.SplitN(path, ":", 2)
	if len(parts) != 2 {
		return path
	}
	return strings.ToLower(parts[0]) + ":" + strings.TrimPrefix(parts[1], "/")
}

func imageSourceStreamsBytes(source opsv1alpha1.UpgradeImageSource) bool {
	return source.URL != "" || source.ConfigMapRef != nil
}

func imageSourceNeedsLifecycle(source opsv1alpha1.UpgradeImageSource) bool {
	return source.Preinstalled != nil || source.DeviceFile != nil || source.LocalPath != ""
}

func upgradeInventoryState(state softwarelifecycle.InventoryState) opsv1alpha1.UpgradeInventoryState {
	return opsv1alpha1.UpgradeInventoryState(state)
}

func installTimeout(up *opsv1alpha1.IOSXESoftwareUpgrade) time.Duration {
	seconds := up.Spec.InstallTimeoutSeconds
	if seconds == 0 {
		seconds = 3600
	}
	return time.Duration(seconds) * time.Second
}

func installTimedOut(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) bool {
	start := up.Status.InstallStartTime
	if start == nil {
		start = up.Status.StartTime
	}
	return start != nil && now.Sub(start.Time) >= installTimeout(up)
}

func remainingInstallTime(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) time.Duration {
	start := up.Status.InstallStartTime
	if start == nil {
		start = up.Status.StartTime
	}
	if start == nil {
		return installTimeout(up)
	}
	remaining := installTimeout(up) - now.Sub(start.Time)
	if remaining <= 0 {
		return time.Nanosecond
	}
	return remaining
}

func maintenanceWindowExpired(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) bool {
	return up.Spec.MaintenanceWindow != nil &&
		up.Spec.MaintenanceWindow.NotAfter != nil &&
		now.After(up.Spec.MaintenanceWindow.NotAfter.Time)
}

func stagingRequestSubmitted(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	if up.Status.StagingRequested {
		return true
	}
	for _, condition := range up.Status.Conditions {
		if condition.Type == conditionTypeStaged && condition.Reason == "StagingRequested" {
			return true
		}
	}
	return false
}

func (r *Reconciler) claimStaging(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	message string,
	now time.Time,
) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
	claimed := false
	var deleting *opsv1alpha1.IOSXESoftwareUpgrade
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXESoftwareUpgrade
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(up), &cur); err != nil {
			if apierrors.IsNotFound(err) {
				deleting = missingUpgradeCleanupCandidate(up, now)
				return nil
			}
			return err
		}
		if !cur.DeletionTimestamp.IsZero() {
			deleting = cur.DeepCopy()
			return nil
		}
		if !upgradeStatusCASMatches(up, &cur) {
			return nil
		}
		if stagingRequestSubmitted(&cur) || terminalUpgradePhase(cur.Status.Phase) {
			return nil
		}
		if cur.Status.InstallStartTime == nil {
			cur.Status.InstallStartTime = &metav1.Time{Time: now}
		}
		cur.Status.StagingRequested = true
		cur.Status.Phase = opsv1alpha1.UpgradePhaseStaging
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		cur.Status.ObservedGeneration = cur.Generation
		r.setCondition(&cur, conditionTypeMutationSettled, metav1.ConditionFalse, "MutationRequested", message, now)
		r.setCondition(&cur, conditionTypeStaged, metav1.ConditionFalse, "StagingRequested", message, now)
		r.setReady(&cur, metav1.ConditionFalse, "StagingRequested", message, now)
		if err := r.Client.Status().Update(ctx, &cur); err != nil {
			return err
		}
		up.Status = *cur.Status.DeepCopy()
		claimed = true
		return nil
	})
	if err != nil {
		return false, nil, fmt.Errorf("claim device-file staging request: %w", err)
	}
	return claimed, deleting, nil
}

func unresolvedInstallAttempt(up *opsv1alpha1.IOSXESoftwareUpgrade) (standby, unresolved bool) {
	if up.Status.StandbySupervisorInstallRequested && !up.Status.StandbySupervisorInstalled {
		return true, true
	}
	if up.Status.PrimarySupervisorInstallRequested && !up.Status.PrimarySupervisorInstalled {
		return false, true
	}
	return false, false
}

func installAttemptRequested(up *opsv1alpha1.IOSXESoftwareUpgrade, standby bool) bool {
	if standby {
		return up.Status.StandbySupervisorInstallRequested
	}
	return up.Status.PrimarySupervisorInstallRequested
}

func (r *Reconciler) claimActivation(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	standby bool,
	reason string,
	message string,
	now time.Time,
) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
	claimed := false
	var deleting *opsv1alpha1.IOSXESoftwareUpgrade
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXESoftwareUpgrade
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(up), &cur); err != nil {
			if apierrors.IsNotFound(err) {
				deleting = missingUpgradeCleanupCandidate(up, now)
				return nil
			}
			return err
		}
		if !cur.DeletionTimestamp.IsZero() {
			deleting = cur.DeepCopy()
			return nil
		}
		if !upgradeStatusCASMatches(up, &cur) {
			return nil
		}
		alreadyRequested := cur.Status.StandbySupervisorActivationRequested
		if !standby {
			alreadyRequested = primaryActivationRequestSubmitted(&cur)
		}
		if alreadyRequested || terminalUpgradePhase(cur.Status.Phase) {
			return nil
		}
		if cur.Status.ActivationStartTime == nil {
			cur.Status.ActivationStartTime = &metav1.Time{Time: now}
		}
		if standby {
			cur.Status.StandbySupervisorActivationRequested = true
		} else {
			cur.Status.PrimarySupervisorActivationRequested = true
		}
		cur.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		cur.Status.ObservedGeneration = cur.Generation
		r.setCondition(&cur, conditionTypeMutationSettled, metav1.ConditionFalse, "MutationRequested", message, now)
		r.setCondition(&cur, conditionTypeActivated, metav1.ConditionFalse, reason, message, now)
		// Activated remains False across the standby and active claims, so the
		// standard condition helper does not refresh LastTransitionTime when only
		// the reason changes. Record the request time explicitly; it is the durable
		// per-RPC grace anchor while ActivationStartTime remains the sequence-wide
		// deadline anchor.
		for i := range cur.Status.Conditions {
			if cur.Status.Conditions[i].Type == conditionTypeActivated && cur.Status.Conditions[i].Reason == reason {
				cur.Status.Conditions[i].LastTransitionTime = metav1.Time{Time: now}
				break
			}
		}
		r.setReady(&cur, metav1.ConditionFalse, reason, message, now)
		if err := r.Client.Status().Update(ctx, &cur); err != nil {
			return err
		}
		up.Status = *cur.Status.DeepCopy()
		claimed = true
		return nil
	})
	if err != nil {
		return false, nil, fmt.Errorf("claim gNOI OS.Activate request: %w", err)
	}
	return claimed, deleting, nil
}

func (r *Reconciler) claimInstallAttempt(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	standby bool,
	now time.Time,
) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
	claimed := false
	var deleting *opsv1alpha1.IOSXESoftwareUpgrade
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXESoftwareUpgrade
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(up), &cur); err != nil {
			if apierrors.IsNotFound(err) {
				deleting = missingUpgradeCleanupCandidate(up, now)
				return nil
			}
			return err
		}
		if !cur.DeletionTimestamp.IsZero() {
			deleting = cur.DeepCopy()
			return nil
		}
		if !upgradeStatusCASMatches(up, &cur) {
			return nil
		}
		if installAttemptRequested(&cur, standby) || terminalUpgradePhase(cur.Status.Phase) {
			return nil
		}
		if standby {
			cur.Status.StandbySupervisorInstallRequested = true
		} else {
			cur.Status.PrimarySupervisorInstallRequested = true
		}
		if cur.Status.InstallStartTime == nil {
			cur.Status.InstallStartTime = &metav1.Time{Time: now}
		}
		cur.Status.ObservedGeneration = cur.Generation
		supervisor := "primary"
		if standby {
			supervisor = "standby"
		}
		message := fmt.Sprintf("gNOI OS.Install request durably recorded for the %s supervisor", supervisor)
		r.setCondition(&cur, conditionTypeMutationSettled, metav1.ConditionFalse, "MutationRequested", message, now)
		r.setCondition(&cur, conditionTypeTransferred, metav1.ConditionFalse, "InstallRequested", message, now)
		r.setReady(&cur, metav1.ConditionFalse, "InstallRequested", message, now)
		if err := r.Client.Status().Update(ctx, &cur); err != nil {
			return err
		}
		up.Status = *cur.Status.DeepCopy()
		claimed = true
		return nil
	})
	if err != nil {
		return false, nil, fmt.Errorf("claim gNOI OS.Install attempt: %w", err)
	}
	return claimed, deleting, nil
}

func (r *Reconciler) pinResolvedImage(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	resolved *ResolvedImage,
	requiresIndividualInstall bool,
	now time.Time,
) (bool, error) {
	pinned := false
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXESoftwareUpgrade
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(up), &cur); err != nil {
			return err
		}
		if !upgradeStatusCASMatches(up, &cur) || terminalUpgradePhase(cur.Status.Phase) || cur.Status.SourceDigest != "" {
			return nil
		}
		cur.Status.SourceDigest = resolved.Digest
		cur.Status.SourceSize = resolved.Size
		cur.Status.IndividualSupervisorInstall = cur.Status.IndividualSupervisorInstall || requiresIndividualInstall
		cur.Status.Message = fmt.Sprintf("resolved and pinned %s (%d bytes); ready for gNOI OS.Install", resolved.Digest, resolved.Size)
		cur.Status.FailureReason = ""
		cur.Status.ObservedGeneration = cur.Generation
		r.setCondition(&cur, conditionTypeImageResolved, metav1.ConditionTrue, "ImagePinned", cur.Status.Message, now)
		r.setReady(&cur, metav1.ConditionFalse, "ImagePinned", cur.Status.Message, now)
		if err := r.Client.Status().Update(ctx, &cur); err != nil {
			return err
		}
		up.Status = *cur.Status.DeepCopy()
		pinned = true
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("pin resolved image: %w", err)
	}
	return pinned, nil
}

func (r *Reconciler) observeUncertainInstall(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	standby bool,
	now time.Time,
) (reconcile.Result, error) {
	if installTimedOut(up, now) {
		if remaining := installResultGraceRemaining(up, now); remaining > 0 {
			return reconcile.Result{RequeueAfter: boundedDuration(time.Second, remaining)}, nil
		}
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallOutcomeUnknown",
			"the dispatched gNOI OS.Install outcome was not proven before the install deadline; its durable attempt marker was retained and the request was not replayed", now)
	}

	supervisor := "primary"
	if standby {
		supervisor = "standby"
	}
	reason := "InstallObservationPending"
	message := fmt.Sprintf("the %s supervisor gNOI OS.Install outcome is unknown; observing without replay until the install deadline", supervisor)
	inventoryState := up.Status.InventoryState
	if r.Lifecycle == nil {
		message += "; no native inventory backend is available"
	} else {
		image, err := r.inspectTarget(ctx, up.Spec.TargetVersion)
		switch {
		case err == nil:
			inventoryState = upgradeInventoryState(image.State)
			if image.State == softwarelifecycle.InventoryStateInProgress {
				reason = "InstallInProgress"
			}
			message = fmt.Sprintf("native inventory reports target %s in state %s after the %s supervisor gNOI OS.Install response was lost; observing without replay until the install deadline",
				image.Version, image.State, supervisor)
		case errors.Is(err, softwarelifecycle.ErrTargetNotFound):
			inventoryState = opsv1alpha1.UpgradeInventoryStateAbsent
			message = fmt.Sprintf("native inventory currently reports target %s absent after the %s supervisor gNOI OS.Install response was lost; absence cannot prove the dispatched request was never accepted, so observing without replay until the install deadline",
				up.Spec.TargetVersion, supervisor)
		default:
			message += ": " + err.Error()
		}
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.InventoryState = inventoryState
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeTransferred, metav1.ConditionFalse, reason, message, now)
		r.setReady(cur, metav1.ConditionFalse, reason, message, now)
	}, reconcile.Result{RequeueAfter: installInventoryPoll})
}

func permanentGNOIError(err error) (string, bool) {
	if gnoi.IsDeviceNotProvisioned(err) {
		return "DeviceNotProvisioned", true
	}
	var unsupported *gnoi.ErrServiceUnsupported
	if errors.As(err, &unsupported) {
		return "GNOIUnsupported", true
	}
	switch status.Code(err) {
	case codes.Unauthenticated:
		return "GNOIUnauthenticated", true
	case codes.PermissionDenied:
		return "GNOIPermissionDenied", true
	case codes.Unimplemented:
		return "GNOIUnsupported", true
	case codes.InvalidArgument:
		return "GNOIInvalidArgument", true
	case codes.FailedPrecondition:
		return "GNOIFailedPrecondition", true
	case codes.NotFound:
		return "GNOINotFound", true
	default:
		return "", false
	}
}

func (r *Reconciler) deviceUpgradeOwner(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (string, error) {
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	var upgrades opsv1alpha1.IOSXESoftwareUpgradeList
	if err := reader.List(ctx, &upgrades, client.InNamespace(up.Namespace)); err != nil {
		return "", fmt.Errorf("list IOSXESoftwareUpgrade device locks: %w", err)
	}
	type candidate struct {
		name    string
		started bool
		at      time.Time
	}
	var candidates []candidate
	for i := range upgrades.Items {
		item := &upgrades.Items[i]
		if item.Spec.DeviceRef.Name != up.Spec.DeviceRef.Name || !item.DeletionTimestamp.IsZero() || terminalUpgradePhase(item.Status.Phase) {
			continue
		}
		if item.Status.Phase == "" || item.Status.Phase == opsv1alpha1.UpgradePhasePending {
			if item.Spec.MaintenanceWindow != nil {
				window := item.Spec.MaintenanceWindow
				if window.NotBefore != nil && now.Before(window.NotBefore.Time) ||
					window.NotAfter != nil && now.After(window.NotAfter.Time) {
					continue
				}
			}
			if validateImageSource(item.Spec.ImageSource) != nil || softwarelifecycle.ValidateTargetVersion(item.Spec.TargetVersion) != nil {
				continue
			}
		}
		at := item.CreationTimestamp.Time
		started := item.Status.Phase != "" && item.Status.Phase != opsv1alpha1.UpgradePhasePending
		if item.Status.StartTime != nil {
			at = item.Status.StartTime.Time
			started = true
		}
		candidates = append(candidates, candidate{name: item.Name, started: started, at: at})
	}
	if len(candidates) == 0 {
		return "", nil
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].started != candidates[j].started {
			return candidates[i].started
		}
		if !candidates[i].at.Equal(candidates[j].at) {
			return candidates[i].at.Before(candidates[j].at)
		}
		return candidates[i].name < candidates[j].name
	})
	return candidates[0].name, nil
}

func (r *Reconciler) ensureMutationLease(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) (bool, reconcile.Result, error) {
	if r.MutationLeaser == nil {
		return r.prepareMutation(ctx, up, now)
	}
	identity := upgradeLeaseIdentity(up)
	guard, err := r.ensureCanonicalLegacyQuarantine(ctx, up, false, now)
	if err != nil {
		requeue, updateErr := r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Message = "waiting for legacy mutation safety scan: " + err.Error()
			cur.Status.FailureReason = ""
			r.setReady(cur, metav1.ConditionFalse, "LegacyMutationGuardError", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: installInventoryPoll})
		return false, requeue, updateErr
	}
	if guard.Risk != nil {
		if guard.CallerOwnsRisk {
			// The guard already renewed the caller's Lease with the legacy risk's
			// complete safety horizon, which is deliberately independent of a
			// future driver's normal operation TTL.
			return r.prepareMutation(ctx, up, now)
		}
		holder := guard.ExistingHolder
		if guard.LeaseOwned || holder == "" {
			holder = guard.Risk.HolderIdentity
		}
		requeue, updateErr := r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Message = fmt.Sprintf("waiting for compatibility quarantine of %s held by %s", guard.Risk, holder)
			cur.Status.FailureReason = ""
			r.setReady(cur, metav1.ConditionFalse, "LegacyMutationQuarantine", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: installInventoryPoll})
		return false, requeue, updateErr
	}
	result, err := r.MutationLeaser.Acquire(ctx, r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily, identity)
	if err != nil {
		requeue, updateErr := r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Message = "waiting for the device software-lifecycle mutation lease: " + err.Error()
			cur.Status.FailureReason = ""
			r.setReady(cur, metav1.ConditionFalse, "MutationLeaseError", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: installInventoryPoll})
		return false, requeue, updateErr
	}
	if !result.Owned {
		holder := result.Holder
		if holder == "" {
			holder = "another operation"
		}
		requeue, updateErr := r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Message = fmt.Sprintf("waiting for device software-lifecycle mutation lease held by %s", holder)
			cur.Status.FailureReason = ""
			r.setReady(cur, metav1.ConditionFalse, "MutationLeaseBlocked", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: installInventoryPoll})
		return false, requeue, updateErr
	}
	return r.prepareMutation(ctx, up, now)
}

func (r *Reconciler) prepareMutation(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (bool, reconcile.Result, error) {
	if r.BeforeMutation != nil {
		if err := r.BeforeMutation(ctx); err != nil {
			result, updateErr := r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				cur.Status.Message = "waiting for device maintenance preparation: " + err.Error()
				r.setReady(cur, metav1.ConditionFalse, "MutationPreparationBlocked", cur.Status.Message, now)
			}, reconcile.Result{RequeueAfter: installInventoryPoll})
			return false, result, updateErr
		}
	}
	return true, reconcile.Result{}, nil
}

func (r *Reconciler) releaseMutationLease(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade) error {
	if r.MutationLeaser == nil {
		return nil
	}
	identity := upgradeLeaseIdentity(up)
	if err := r.MutationLeaser.Release(ctx, r.mutationLeaseDeviceKey(), devicecoordination.MutationLeaseFamily, identity); err != nil {
		return fmt.Errorf("release software-lifecycle mutation lease: %w", err)
	}
	return nil
}

func upgradeLeaseIdentity(up *opsv1alpha1.IOSXESoftwareUpgrade) string {
	return mutationguard.UpgradeHolderIdentity(up)
}

func (r *Reconciler) mutationLeaseDeviceKey() string {
	return devicecoordination.DeviceKey(r.DeviceNamespace, r.DeviceName)
}

// ensureExecutionModel distinguishes workflows created under the durable
// at-most-once state machine from objects left in flight by released
// controllers. Pending and Resolving are safe adoption points because no
// device mutation has been submitted yet. Later phases are quarantined rather
// than replayed because the old status cannot prove whether IOS XE accepted a
// request before the controller stopped.
func (r *Reconciler) ensureExecutionModel(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) (bool, reconcile.Result, error) {
	if up.Status.ExecutionModel == opsv1alpha1.UpgradeExecutionModelAtMostOnceV1 {
		return false, reconcile.Result{}, nil
	}
	switch up.Status.Phase {
	case "", opsv1alpha1.UpgradePhasePending, opsv1alpha1.UpgradePhaseResolving:
		result, err := r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.ExecutionModel = opsv1alpha1.UpgradeExecutionModelAtMostOnceV1
			if cur.Status.Phase == "" {
				cur.Status.Phase = opsv1alpha1.UpgradePhasePending
			}
		}, reconcile.Result{RequeueAfter: time.Second})
		return true, result, err
	default:
		result := reconcile.Result{RequeueAfter: installInventoryPoll}
		owned, holder, ttl, err := r.acquireQuarantineLease(ctx, up, false, now)
		if err != nil {
			return true, result, fmt.Errorf("hold mutation lease for markerless upgrade: %w", err)
		}
		if !owned {
			log.G(ctx).WithField("softwareUpgrade", client.ObjectKeyFromObject(up).String()).
				WithField("leaseHolder", holder).
				Warn("markerless upgrade is waiting for the shared mutation quarantine lease")
			return true, result, nil
		}
		result.RequeueAfter = mutationguard.RenewalInterval(ttl)
		message := fmt.Sprintf("upgrade phase %s predates the durable at-most-once execution marker; device mutation outcome is unknown and will not be replayed", up.Status.Phase)
		result, err = r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed,
			"LegacyStateOutcomeUnknown", message, now)
		return true, result, err
	}
}

// holdLegacyTerminalQuarantine keeps a bounded fence around terminal status
// written by a released controller when that status cannot prove the device
// mutation has stopped converging. The status itself remains untouched.
func (r *Reconciler) holdLegacyTerminalQuarantine(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) (reconcile.Result, error) {
	result := reconcile.Result{RequeueAfter: installInventoryPoll}
	owned, holder, ttl, err := r.acquireQuarantineLease(ctx, up, false, now)
	if err != nil {
		return result, fmt.Errorf("hold mutation lease for terminal legacy upgrade: %w", err)
	}
	if owned {
		result.RequeueAfter = mutationguard.RenewalInterval(ttl)
		return result, nil
	}
	log.G(ctx).WithField("softwareUpgrade", client.ObjectKeyFromObject(up).String()).
		WithField("leaseHolder", holder).
		Warn("terminal legacy upgrade is waiting for the shared mutation quarantine lease")
	return result, nil
}

// holdUnsupportedExecutionModel preserves status written by a newer controller
// while preventing this controller from releasing its safety fence or issuing
// any device call. It runs before terminal handling, but only after the cleanup
// finalizer is durable, so even a future state whose phase looks terminal to
// this binary is left intact for a compatible controller.
func (r *Reconciler) holdUnsupportedExecutionModel(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) (reconcile.Result, error) {
	message := fmt.Sprintf("upgrade execution model %q is not supported by this controller; preserving status and refusing device work", up.Status.ExecutionModel)
	log.G(ctx).WithField("softwareUpgrade", client.ObjectKeyFromObject(up).String()).
		WithField("executionModel", up.Status.ExecutionModel).
		WithField("phase", up.Status.Phase).
		Warn(message)
	r.emitEvent(up, corev1.EventTypeWarning, "UnsupportedExecutionModel", message)
	result := reconcile.Result{RequeueAfter: installInventoryPoll}
	owned, holder, ttl, err := r.acquireQuarantineLease(ctx, up, false, now)
	if err != nil {
		return result, fmt.Errorf("hold mutation lease for unsupported upgrade execution model: %w", err)
	}
	if owned {
		result.RequeueAfter = mutationguard.RenewalInterval(ttl)
	}
	if !owned {
		log.G(ctx).WithField("softwareUpgrade", client.ObjectKeyFromObject(up).String()).
			WithField("leaseHolder", holder).
			Warn("unsupported upgrade execution model is waiting for the shared mutation lease")
	}
	return result, nil
}

func (r *Reconciler) acquireQuarantineLease(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	deleting bool,
	now time.Time,
) (bool, string, time.Duration, error) {
	if r.MutationLeaser == nil {
		return false, "", 0, errors.New("software-lifecycle mutation leaser is required for outcome quarantine")
	}
	result, err := r.ensureCanonicalLegacyQuarantine(ctx, up, deleting, now)
	if err != nil {
		return false, "", 0, err
	}
	if result.Risk == nil {
		return false, "", 0, errors.New("quarantined upgrade was absent from the compatibility safety scan")
	}
	holder := result.ExistingHolder
	if result.LeaseOwned || holder == "" {
		holder = result.Risk.HolderIdentity
	}
	return result.CallerOwnsRisk, holder, result.Risk.LeaseTTL, nil
}

func (r *Reconciler) ensureCanonicalLegacyQuarantine(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	deleting bool,
	now time.Time,
) (mutationguard.Result, error) {
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	if deleting {
		return mutationguard.EnsureCanonicalQuarantineForDeletion(ctx, reader, r.MutationLeaser,
			up.Namespace, r.DeviceName, r.mutationLeaseDeviceKey(), upgradeLeaseIdentity(up), now)
	}
	return mutationguard.EnsureCanonicalQuarantine(ctx, reader, r.MutationLeaser,
		up.Namespace, r.DeviceName, r.mutationLeaseDeviceKey(), upgradeLeaseIdentity(up), now)
}

func upgradePhaseMayHaveDispatchedMutation(phase opsv1alpha1.UpgradePhase) bool {
	switch phase {
	case "", opsv1alpha1.UpgradePhasePending,
		opsv1alpha1.UpgradePhaseResolving,
		opsv1alpha1.UpgradePhaseSucceeded,
		opsv1alpha1.UpgradePhaseStagedForNextBoot,
		opsv1alpha1.UpgradePhasePreflightFailed,
		opsv1alpha1.UpgradePhaseRolledBack,
		opsv1alpha1.UpgradePhaseCancelled:
		return false
	default:
		// Unknown markerless states are mutation-capable by default. The
		// execution model must be bumped before a future controller introduces
		// a new phase that can safely be interpreted more narrowly.
		return true
	}
}

func upgradeMutationSubmitted(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	return up != nil && (up.Status.FailureReason == "LegacyStateOutcomeUnknown" ||
		unsupportedExecutionModel(up) ||
		(up.Status.ExecutionModel == "" && upgradePhaseMayHaveDispatchedMutation(up.Status.Phase)) ||
		stagingRequestSubmitted(up) ||
		up.Status.PrimarySupervisorInstallRequested ||
		up.Status.StandbySupervisorInstallRequested ||
		up.Status.StandbySupervisorActivationRequested ||
		primaryActivationRequestSubmitted(up) ||
		rollbackRequestSubmitted(up))
}

func upgradeStateRequiresQuarantine(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) bool {
	return mutationguard.UpgradeRequiresQuarantineAt(up, now)
}

func terminalUpgradePhase(phase opsv1alpha1.UpgradePhase) bool {
	return phase == opsv1alpha1.UpgradePhaseSucceeded ||
		phase == opsv1alpha1.UpgradePhaseStagedForNextBoot ||
		isTerminalFailurePhase(phase)
}

func (r *Reconciler) runTransferring(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	if attemptedStandby, unresolved := unresolvedInstallAttempt(up); unresolved {
		return r.observeUncertainInstall(ctx, up, attemptedStandby, now)
	}
	if r.GNOI == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "NoGNOIProvider",
			"gnoi provider not configured on reconciler", now)
	}
	gnoiClient, err := r.gnoiClient(ctx)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		if reason, permanent := permanentGNOIError(err); permanent {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, reason, err.Error(), now)
		}
		return r.waitForTransferPreflight(ctx, up, "DeviceUnreachable",
			fmt.Sprintf("waiting for gNOI client before image transfer: %s", err.Error()), now)
	}
	verify, err := verifyOS(ctx, gnoiClient)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		if reason, permanent := permanentGNOIError(err); permanent {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, reason, err.Error(), now)
		}
		return r.waitForTransferPreflight(ctx, up, "VerifyPending",
			fmt.Sprintf("waiting for gNOI OS.Verify before image transfer: %s", err.Error()), now)
	}
	if versionMatches(verify.Version, up.Spec.TargetVersion) {
		if ready, detail := allRequiredSupervisorsRunTarget(verify, up.Spec.TargetVersion,
			up.Status.IndividualSupervisorInstall || verify.IndividualSupervisorInstall); !ready {
			if verify.Standby.State == gnoi.StandbyStateUnavailable {
				return r.waitForSupervisorTarget(ctx, up, opsv1alpha1.UpgradePhaseTransferring, detail, now)
			}
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "SupervisorTargetMismatch", detail, now)
		}
		return r.updateTerminalStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Phase = opsv1alpha1.UpgradePhaseSucceeded
			cur.Status.RunningVersion = verify.Version
			cur.Status.CompletionTime = &metav1.Time{Time: now}
			cur.Status.Message = fmt.Sprintf("target version %s already running; install skipped", verify.Version)
			r.setReady(cur, metav1.ConditionTrue, "AlreadyRunning", cur.Status.Message, now)
		}, reconcile.Result{})
	}
	requiresIndividualInstall := up.Status.IndividualSupervisorInstall || verify.IndividualSupervisorInstall
	if up.Spec.Strategy == opsv1alpha1.UpgradeStrategyNoReboot && requiresIndividualInstall {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "IndividualSupervisorNoRebootUnsupported",
			"NoReboot cannot safely prove the required per-supervisor boot sequence; use strategy Reload", now)
	}
	standby := requiresIndividualInstall && up.Status.PrimarySupervisorInstalled
	if installTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallTimeout",
			fmt.Sprintf("image resolution did not complete within %s", installTimeout(up)), now)
	}
	resolveCtx, cancelResolve := context.WithTimeout(ctx, remainingInstallTime(up, now))
	resolved, err := r.ImageResolver.Resolve(resolveCtx, up.Namespace, up.Spec.ImageSource)
	resolveDeadlineExceeded := errors.Is(resolveCtx.Err(), context.DeadlineExceeded)
	cancelResolve()
	// Resolution may download and hash a multi-gigabyte image. Refresh the
	// reconciliation clock before evaluating its result or making any decision
	// that can lead to a device mutation.
	now = r.now()
	defer func() {
		if resolved != nil && resolved.Cleanup != nil {
			_ = resolved.Cleanup()
		}
	}()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			if ctx.Err() != nil {
				return reconcile.Result{}, ctx.Err()
			}
		}
		if resolveDeadlineExceeded || installTimedOut(up, now) {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallTimeout",
				fmt.Sprintf("image resolution did not complete within %s", installTimeout(up)), now)
		}
		if ctx.Err() != nil {
			return reconcile.Result{}, ctx.Err()
		}
		if IsRetryableResolveError(err) {
			message := fmt.Sprintf("transient image resolution failure; retrying within the install deadline: %s", err.Error())
			return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				cur.Status.Message = message
				cur.Status.FailureReason = ""
				r.setCondition(cur, conditionTypeImageResolved, metav1.ConditionFalse, "ImageResolveRetry", message, now)
				r.setReady(cur, metav1.ConditionFalse, "ImageResolveRetry", message, now)
			}, reconcile.Result{RequeueAfter: boundedDuration(installInventoryPoll, remainingInstallTime(up, now))})
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return reconcile.Result{}, err
		}
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ImageResolveFailed", err.Error(), now)
	}
	if resolveDeadlineExceeded {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallTimeout",
			fmt.Sprintf("image resolution did not complete within %s", installTimeout(up)), now)
	}
	if resolved == nil || resolved.Reader == nil || resolved.Size <= 0 || !validContentDigest(resolved.Digest) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InvalidResolvedImage",
			"image resolver returned no content, positive size, or valid SHA256 content address", now)
	}
	if up.Spec.ImageSource.URL != "" && resolved.Digest != "sha256:"+up.Spec.ImageSource.SHA256 {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "ImageDigestMismatch",
			fmt.Sprintf("resolved URL image digest %s does not match declared digest sha256:%s", resolved.Digest, up.Spec.ImageSource.SHA256), now)
	}
	if maintenanceWindowExpired(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "MaintenanceWindowExpired",
			"maintenance window closed before gNOI OS.Install could be submitted", now)
	}
	if installTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallTimeout",
			fmt.Sprintf("gNOI OS.Install did not complete within %s", installTimeout(up)), now)
	}
	if up.Status.SourceDigest == "" {
		pinned, err := r.pinResolvedImage(ctx, up, resolved, requiresIndividualInstall, now)
		if err != nil {
			return reconcile.Result{}, err
		}
		if !pinned {
			return reconcile.Result{RequeueAfter: time.Second}, nil
		}
	}
	if resolved.Digest != up.Status.SourceDigest || resolved.Size != up.Status.SourceSize {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "ImageSourceChanged",
			fmt.Sprintf("resolved image changed after it was pinned: got %s/%d bytes, want %s/%d bytes",
				resolved.Digest, resolved.Size, up.Status.SourceDigest, up.Status.SourceSize), now)
	}
	// The lease acquired at the start of reconciliation may have expired or
	// changed owners during resolution. Validate it immediately before the
	// durable Install claim and dispatch.
	owned, result, err := r.ensureMutationLease(ctx, up, now)
	if err != nil || !owned {
		return result, err
	}
	claimed, deleting, err := r.claimInstallAttempt(ctx, up, standby, now)
	if err != nil {
		return reconcile.Result{}, err
	}
	if deleting != nil {
		return r.handleDelete(ctx, deleting, now)
	}
	if !claimed {
		return reconcile.Result{RequeueAfter: time.Second}, nil
	}
	log.G(ctx).WithField("softwareUpgrade", client.ObjectKeyFromObject(up).String()).
		WithField("targetVersion", up.Spec.TargetVersion).
		WithField("standbySupervisor", standby).
		Warn("dispatching gNOI OS.Install")
	installCtx, cancel := context.WithTimeout(ctx, remainingInstallTime(up, now))
	defer cancel()
	progress, err := gnoiClient.Install(installCtx, resolved.Reader, gnoi.InstallOpts{
		// An empty version forces the target to consume the digest-verified
		// bytes instead of satisfying this content source from a same-version
		// package that was already present on the device.
		Version:           "",
		PackageSize:       uint64(resolved.Size),
		StandbySupervisor: standby,
	})
	if err != nil {
		now = r.now()
		r.resetGNOIClientIfTransient(ctx, err)
		return r.handleInstallErr(ctx, up, err, now)
	}
	var validated *gnoi.InstallValidated
	transferred := false
	contentProven := false
	for ev := range progress {
		switch {
		case ev.Err != nil:
			now = r.now()
			r.resetGNOIClientIfTransient(ctx, ev.Err)
			return r.handleInstallErr(ctx, up, ev.Err, now)
		case ev.TransferReady:
			transferred = true
			contentProven = true
		case ev.TransferProgress != nil:
			transferred = true
			r.updateTransferProgress(ctx, up, ev.TransferProgress.BytesReceived, resolved.Size, now)
		case ev.SyncProgress != nil:
			transferred = true
			// The standby may safely synchronize from the primary only after
			// this operation durably installed the pinned content there.
			contentProven = standby && up.Status.PrimarySupervisorInstalled
			r.updateSyncProgress(ctx, up, ev.SyncProgress.PercentageTransferred, standby, now)
		case ev.Validated != nil:
			validated = ev.Validated
		}
	}
	// Install streams can run for minutes. Use the completion time for all
	// validation, conditions, and the subsequent activation transition.
	now = r.now()
	if validated == nil {
		return r.handleInstallErr(ctx, up, errors.New("gnoi Install: stream ended without Validated"), now)
	}
	if !contentProven {
		return r.terminalAfterMutation(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "ImageContentNotTransferred",
			"device validated a version without consuming the pinned image content; refusing to activate unproven same-version bytes", now)
	}
	if strings.TrimSpace(validated.Version) == "" {
		return r.terminalAfterMutation(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "EmptyValidatedVersion",
			"device returned an empty version from gNOI OS.Install", now)
	}
	if !versionMatches(validated.Version, up.Spec.TargetVersion) {
		return r.terminalAfterMutation(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "VersionMismatch",
			fmt.Sprintf("device validated version %q but spec targets %q", validated.Version, up.Spec.TargetVersion), now)
	}
	if standby && up.Status.ValidatedVersion != "" && validated.Version != up.Status.ValidatedVersion {
		return r.terminalAfterMutation(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "SupervisorValidatedVersionMismatch",
			fmt.Sprintf("standby supervisor validated exact version %q, but the primary supervisor validated %q",
				validated.Version, up.Status.ValidatedVersion), now)
	}
	if requiresIndividualInstall && !standby {
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.IndividualSupervisorInstall = true
			cur.Status.PrimarySupervisorInstalled = true
			cur.Status.ValidatedVersion = validated.Version
			cur.Status.Message = fmt.Sprintf("primary supervisor validated %s; installing standby supervisor", validated.Version)
			cur.Status.FailureReason = ""
			r.setCondition(cur, conditionTypeMutationSettled, metav1.ConditionTrue, "InstallCompleted", cur.Status.Message, now)
			r.setCondition(cur, conditionTypeValidated, metav1.ConditionFalse, "StandbyInstallPending", cur.Status.Message, now)
			r.setReady(cur, metav1.ConditionFalse, "StandbyInstallPending", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: time.Second})
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		if cur.Status.StartTime == nil {
			cur.Status.StartTime = &metav1.Time{Time: now}
		}
		cur.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		cur.Status.ValidatedVersion = validated.Version
		cur.Status.IndividualSupervisorInstall = requiresIndividualInstall
		if standby {
			cur.Status.StandbySupervisorInstalled = true
		} else {
			cur.Status.PrimarySupervisorInstalled = true
		}
		cur.Status.Message = fmt.Sprintf("device validated %s, activating", validated.Version)
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeMutationSettled, metav1.ConditionTrue, "InstallCompleted", cur.Status.Message, now)
		markTransferComplete(cur)
		transferReason := "AlreadyInstalled"
		transferMessage := "gNOI OS.Install reported that the content was already installed"
		if transferred {
			transferReason = "Transferred"
			transferMessage = "image transfer or supervisor synchronization completed and the install stream closed cleanly"
		}
		r.setCondition(cur, conditionTypeTransferred, metav1.ConditionTrue, transferReason, transferMessage, now)
		r.setCondition(cur, conditionTypeValidated, metav1.ConditionTrue, "Validated",
			fmt.Sprintf("device validated image version %s", validated.Version), now)
		r.setCondition(cur, conditionTypeActivated, metav1.ConditionFalse, "ActivationPending",
			"waiting to submit gNOI OS.Activate", now)
		r.setReady(cur, metav1.ConditionFalse, "Validated", cur.Status.Message, now)
	}, reconcile.Result{RequeueAfter: time.Second})
}

func (r *Reconciler) waitForTransferPreflight(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, reason, message string, now time.Time) (reconcile.Result, error) {
	if installTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallTimeout",
			fmt.Sprintf("gNOI transfer preflight did not become ready within %s: %s", installTimeout(up), message), now)
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		if cur.Status.StartTime == nil {
			cur.Status.StartTime = &metav1.Time{Time: now}
		}
		cur.Status.Phase = opsv1alpha1.UpgradePhaseTransferring
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeTransferred, metav1.ConditionFalse, reason, message, now)
		r.setReady(cur, metav1.ConditionFalse, reason, message, now)
	}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
}

func (r *Reconciler) handleInstallErr(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, err error, now time.Time) (reconcile.Result, error) {
	var iErr *gnoi.InstallError
	if errors.As(err, &iErr) {
		switch iErr.Type {
		case gnoi.InstallErrorInstallInProgress:
			return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				cur.Status.Phase = opsv1alpha1.UpgradePhaseTransferInterrupted
				cur.Status.InventoryState = opsv1alpha1.UpgradeInventoryStateInProgress
				cur.Status.Message = "device reports an existing install in progress; observing it as an uncertain gNOI install without consuming transfer retries"
				cur.Status.FailureReason = ""
				r.setCondition(cur, conditionTypeTransferred, metav1.ConditionFalse, "InstallInProgress", cur.Status.Message, now)
				r.setReady(cur, metav1.ConditionFalse, "InstallInProgress", cur.Status.Message, now)
			}, reconcile.Result{RequeueAfter: installInventoryPoll})
		case gnoi.InstallErrorIncompatible,
			gnoi.InstallErrorTooLarge,
			gnoi.InstallErrorParseFail,
			gnoi.InstallErrorIntegrityFail,
			gnoi.InstallErrorInstallRunPackage,
			gnoi.InstallErrorNotSupportedBackup:
			// Hard failure — no retry.
			return r.terminalAfterMutation(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, string(iErr.Type), iErr.Error(), now)
		case gnoi.InstallErrorUnexpectedSwitchovr,
			gnoi.InstallErrorSyncFail:
			// The device explicitly reported a failure after install work may
			// have begun. Observe inventory without replaying the request.
		default:
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallFailed", iErr.Error(), now)
		}
	}
	if reason, permanent := permanentGNOIError(err); permanent {
		return r.terminalAfterMutation(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, reason, err.Error(), now)
	}
	// Once the request marker is durable, an untyped transport, stream, parser,
	// or local-reader failure cannot prove that IOS XE rejected the request.
	// Keep the marker and observe until the deadline; even repeated inventory
	// absence is insufficient evidence that replay is safe.
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseTransferInterrupted
		cur.Status.Message = fmt.Sprintf("gNOI OS.Install outcome is unknown; observing without replay until the install deadline: %s", err.Error())
		cur.Status.FailureReason = "TransferInterrupted"
		r.setReady(cur, metav1.ConditionFalse, "TransferInterrupted", cur.Status.Message, now)
	}, reconcile.Result{RequeueAfter: installInventoryPoll})
}

func (r *Reconciler) runTransferInterrupted(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	standby, unresolved := unresolvedInstallAttempt(up)
	if !unresolved {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "InstallAttemptMarkerMissing",
			"interrupted gNOI OS.Install has no durable in-flight attempt marker; refusing an unsafe replay", now)
	}
	return r.observeUncertainInstall(ctx, up, standby, now)
}

func (r *Reconciler) runActivating(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	individual := up.Status.IndividualSupervisorInstall
	noReboot := up.Spec.Strategy == opsv1alpha1.UpgradeStrategyNoReboot
	if individual && noReboot {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "IndividualSupervisorNoRebootUnsupported",
			"NoReboot cannot safely prove the required per-supervisor boot sequence; use strategy Reload", now)
	}
	if individual && !up.Status.StandbySupervisorActivated {
		if up.Status.StandbySupervisorActivationRequested {
			if remaining := activationRPCGraceRemaining(up, true, now); remaining > 0 {
				return reconcile.Result{RequeueAfter: boundedDuration(time.Second, remaining)}, nil
			}
			return r.awaitActivationConvergence(ctx, up, true, "StandbyActivationRecorded",
				"recovering a recorded standby activation request without replaying it", now)
		}
		return r.submitActivation(ctx, up, true, false, now)
	}
	if primaryActivationRequestSubmitted(up) {
		if noReboot {
			if up.Status.NoRebootActivationAccepted {
				return r.verifyNoRebootActivation(ctx, up, now)
			}
		}
		if remaining := activationRPCGraceRemaining(up, false, now); remaining > 0 {
			return reconcile.Result{RequeueAfter: boundedDuration(time.Second, remaining)}, nil
		}
		if noReboot {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ActivationOutcomeUnknown",
				"a NoReboot activation intent was persisted before the device response was recorded; refusing to replay an activation that cannot be verified from the running version", now)
		}
		return r.awaitActivationConvergence(ctx, up, false, "ActivationRecorded",
			"recovering a recorded activation request without replaying it", now)
	}
	return r.submitActivation(ctx, up, false, noReboot, now)
}

func (r *Reconciler) submitActivation(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	standby bool,
	noReboot bool,
	now time.Time,
) (reconcile.Result, error) {
	if maintenanceWindowExpired(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "MaintenanceWindowExpired",
			"maintenance window closed before gNOI OS.Activate could be submitted", now)
	}
	if activationControlTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ActivationControlTimeout",
			fmt.Sprintf("initial activation control did not become ready within %s", rebootTimeout(up)), now)
	}
	if upgradeWaitStart(up) != nil && upgradeTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ActivationControlTimeout",
			fmt.Sprintf("activation sequence did not complete within %s; refusing to submit another activation", rebootTimeout(up)), now)
	}
	if r.GNOI == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "NoGNOIProvider",
			"gNOI provider is required for activation", now)
	}
	gnoiClient, err := r.gnoiClient(ctx)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		if reason, permanent := permanentGNOIError(err); permanent {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, reason, err.Error(), now)
		}
		return r.waitForActivationControl(ctx, up, "waiting for a gNOI client before activation: "+err.Error(), now)
	}
	now = r.now()
	if activationControlTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ActivationControlTimeout",
			fmt.Sprintf("initial activation control did not become ready within %s", rebootTimeout(up)), now)
	}
	if upgradeWaitStart(up) != nil && upgradeTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ActivationControlTimeout",
			fmt.Sprintf("activation sequence did not complete within %s; refusing to submit another activation", rebootTimeout(up)), now)
	}
	activateVersion := up.Status.ValidatedVersion
	if strings.TrimSpace(activateVersion) == "" {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "ValidatedVersionMissing",
			"activation requires an exact version returned by gNOI OS.Install or the platform install inventory", now)
	}
	if maintenanceWindowExpired(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "MaintenanceWindowExpired",
			"maintenance window closed before gNOI OS.Activate could be submitted", now)
	}
	supervisor := "active"
	conditionReason := "ActivationRequested"
	if standby {
		supervisor = "standby"
		conditionReason = "StandbyActivationRequested"
	}
	activationMessage := fmt.Sprintf("submitting gNOI OS.Activate for exact version %s on the %s supervisor", activateVersion, supervisor)
	if noReboot {
		activationMessage += " with NoReboot=true"
	} else {
		activationMessage += "; reload expected"
	}
	claimed, deleting, err := r.claimActivation(ctx, up, standby, conditionReason, activationMessage, now)
	if err != nil {
		return reconcile.Result{}, err
	}
	if deleting != nil {
		return r.handleDelete(ctx, deleting, now)
	}
	if !claimed {
		return reconcile.Result{RequeueAfter: time.Second}, nil
	}
	r.emitEvent(up, corev1.EventTypeNormal, conditionReason, activationMessage)
	activateCtx, cancel := context.WithTimeout(ctx,
		boundedDuration(activationRPCTimeout, remainingActivationTime(up, now)))
	log.G(ctx).WithField("softwareUpgrade", client.ObjectKeyFromObject(up).String()).
		WithField("version", activateVersion).
		WithField("standbySupervisor", standby).
		WithField("noReboot", noReboot).
		Warn("dispatching gNOI OS.Activate")
	activateErr := gnoiClient.Activate(activateCtx, gnoi.ActivateOpts{
		Version:           activateVersion,
		StandbySupervisor: standby,
		NoReboot:          noReboot,
	})
	cancel()
	now = r.now()
	if activateErr != nil {
		if activationMayHaveStarted(activateErr) {
			r.resetGNOIClientIfTransient(ctx, activateErr)
			if noReboot {
				return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ActivationOutcomeUnknown",
					fmt.Sprintf("NoReboot activation response was lost after requesting %s; refusing to replay: %s", activateVersion, activateErr), now)
			}
			return r.awaitActivationConvergence(ctx, up, standby, "ActivationResponseLost",
				fmt.Sprintf("gNOI OS.Activate did not return cleanly after requesting version %s on the %s supervisor: %s", activateVersion, supervisor, activateErr), now)
		}
		r.resetGNOIClientIfTransient(ctx, activateErr)
		reason := "ActivateFailed"
		if standby {
			reason = "StandbyActivateFailed"
		}
		return r.terminalAfterMutation(ctx, up, opsv1alpha1.UpgradePhaseFailed, reason, activateErr.Error(), now)
	}
	if noReboot {
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Phase = opsv1alpha1.UpgradePhaseActivating
			cur.Status.NoRebootActivationAccepted = true
			cur.Status.FailureReason = ""
			cur.Status.Message = fmt.Sprintf("NoReboot activation accepted for %s; verifying control-plane reachability", activateVersion)
			r.setCondition(cur, conditionTypeActivated, metav1.ConditionTrue, "NoRebootAccepted", cur.Status.Message, now)
			r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "NoRebootVerificationPending",
				"waiting for read-only OS.Verify after the accepted NoReboot request", now)
			r.setReady(cur, metav1.ConditionFalse, "NoRebootVerificationPending", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: time.Second})
	}
	if standby {
		return r.awaitActivationConvergence(ctx, up, true, "StandbyActivationAccepted",
			fmt.Sprintf("activate accepted for standby supervisor version %s", activateVersion), now)
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

func (r *Reconciler) verifyNoRebootActivation(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) (reconcile.Result, error) {
	if r.GNOI == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "NoGNOIProvider",
			"gNOI provider is required to verify the accepted NoReboot request", now)
	}
	gnoiClient, err := r.gnoiClient(ctx)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		if reason, permanent := permanentGNOIError(err); permanent {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, reason, err.Error(), now)
		}
		return r.waitForActivationControl(ctx, up, "waiting to verify accepted NoReboot activation: "+err.Error(), now)
	}
	verify, err := verifyOS(ctx, gnoiClient)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		if reason, permanent := permanentGNOIError(err); permanent {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, reason, err.Error(), now)
		}
		return r.waitForActivationControl(ctx, up, "waiting for OS.Verify after accepted NoReboot activation: "+err.Error(), now)
	}
	if failure := strings.TrimSpace(verify.ActivationFailMessage); failure != "" {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ActivationRejected",
			fmt.Sprintf("OS.Verify reports activation failure: %s", failure), now)
	}
	requiresIndividual := up.Status.IndividualSupervisorInstall || verify.IndividualSupervisorInstall
	if requiresIndividual {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "IndividualSupervisorNoRebootUnsupported",
			"OS.Verify reports that individual-supervisor handling is required after NoReboot activation; refusing to infer a safe staged state", now)
	}
	if ready, _ := allRequiredSupervisorsRunTarget(verify, up.Spec.TargetVersion, requiresIndividual); ready {
		return r.updateTerminalStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Phase = opsv1alpha1.UpgradePhaseSucceeded
			cur.Status.RunningVersion = verify.Version
			cur.Status.FailureReason = ""
			cur.Status.Message = fmt.Sprintf("device already runs target version %s after NoReboot activation", verify.Version)
			cur.Status.CompletionTime = &metav1.Time{Time: now}
			r.setCondition(cur, conditionTypeVerified, metav1.ConditionTrue, "Verified", cur.Status.Message, now)
			r.setReady(cur, metav1.ConditionTrue, "Succeeded", cur.Status.Message, now)
		}, reconcile.Result{})
	}
	if strings.TrimSpace(up.Status.PreviousVersion) == "" || !versionMatches(verify.Version, up.Status.PreviousVersion) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "UnexpectedRunningVersion",
			fmt.Sprintf("OS.Verify reports running version %q after NoReboot activation; expected target %q or captured previous version %q",
				verify.Version, up.Spec.TargetVersion, up.Status.PreviousVersion), now)
	}
	message := fmt.Sprintf("target version %s is staged for the next boot; device remains on running version %s",
		up.Status.ValidatedVersion, verify.Version)
	return r.updateTerminalStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseStagedForNextBoot
		cur.Status.RunningVersion = verify.Version
		cur.Status.FailureReason = ""
		cur.Status.Message = message
		cur.Status.CompletionTime = &metav1.Time{Time: now}
		r.setCondition(cur, conditionTypeActivated, metav1.ConditionTrue, "StagedForNextBoot", message, now)
		r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "RebootRequired",
			"the target version is not yet the running OS; a separately authorized reboot is required", now)
		r.setReady(cur, metav1.ConditionTrue, "StagedForNextBoot", message, now)
	}, reconcile.Result{})
}

func (r *Reconciler) waitForActivationControl(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	message string,
	now time.Time,
) (reconcile.Result, error) {
	if upgradeTimedOut(up, now) || activationControlTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "ActivationControlTimeout", message, now)
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		if upgradeWaitStart(cur) == nil && cur.Status.ActivationControlStartTime == nil {
			cur.Status.ActivationControlStartTime = &metav1.Time{Time: now}
		}
		cur.Status.Phase = opsv1alpha1.UpgradePhaseActivating
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setReady(cur, metav1.ConditionFalse, "ActivationControlPending", message, now)
	}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
}

func (r *Reconciler) awaitActivationConvergence(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	standby bool,
	reason string,
	message string,
	now time.Time,
) (reconcile.Result, error) {
	timeout := up.Spec.RebootTimeoutSeconds
	if timeout == 0 {
		timeout = 1800
	}
	waitTarget := "device"
	readyReason := "Rebooting"
	if standby {
		waitTarget = "standby supervisor"
		readyReason = "StandbyRebooting"
	}
	message = fmt.Sprintf("%s; waiting up to %ds for the %s to report the target version", message, timeout, waitTarget)
	r.emitEvent(up, corev1.EventTypeNormal, reason, message)
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		conditionStatus := metav1.ConditionTrue
		if standby {
			conditionStatus = metav1.ConditionFalse
		}
		r.setCondition(cur, conditionTypeActivated, conditionStatus, reason, message, now)
		r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionFalse, "ReloadExpected", message, now)
		r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "VerifyPending",
			"waiting for post-activation supervisor verification", now)
		r.setReady(cur, metav1.ConditionFalse, readyReason, message, now)
	}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
}

func (r *Reconciler) runAwaitingReachability(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	if r.GNOI == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "NoGNOIProvider",
			"gNOI provider is required while waiting for activation", now)
	}
	gnoiClient, err := r.gnoiClient(ctx)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		// Conn missing usually means the device is still rebooting; backoff.
		return r.requeueAwaitingReachability(ctx, up, err, now)
	}
	if _, err := deviceTime(ctx, gnoiClient); err != nil {
		if isUnsupportedSystemService(err) {
			return r.verifyReachableDevice(ctx, up, gnoiClient, "device gNOI endpoint is reachable; system service unsupported", now)
		}
		r.resetGNOIClientIfTransient(ctx, err)
		return r.requeueAwaitingReachability(ctx, up, err, now)
	}
	return r.verifyReachableDevice(ctx, up, gnoiClient, "device is reachable", now)
}

func (r *Reconciler) verifyReachableDevice(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	client *gnoi.Client,
	reachableMessage string,
	now time.Time,
) (reconcile.Result, error) {
	res, err := verifyOS(ctx, client)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		if reason, permanent := permanentGNOIError(err); permanent {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, reason, err.Error(), now)
		}
		return r.requeueAwaitingReachability(ctx, up, fmt.Errorf("%s but OS.Verify is not ready: %w", reachableMessage, err), now)
	}
	if up.Status.IndividualSupervisorInstall &&
		up.Status.StandbySupervisorActivationRequested &&
		!up.Status.StandbySupervisorActivated {
		return r.verifyStandbyActivation(ctx, up, res, reachableMessage, now)
	}
	if strings.TrimSpace(res.ActivationFailMessage) != "" {
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Phase = opsv1alpha1.UpgradePhaseVerifying
			cur.Status.RunningVersion = res.Version
			cur.Status.Message = fmt.Sprintf("OS.Verify reports activation failure: %s", res.ActivationFailMessage)
			cur.Status.FailureReason = ""
			r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionTrue, "DeviceReachable", reachableMessage, now)
			r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "ActivationFailed", cur.Status.Message, now)
			r.setReady(cur, metav1.ConditionFalse, "ActivationFailed", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: time.Second})
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
	message := fmt.Sprintf("activation convergence deadline elapsed while device reports %s; running final verification for target %s",
		res.Version, up.Spec.TargetVersion)
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseVerifying
		cur.Status.RunningVersion = res.Version
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionTrue, "DeviceReachable", reachableMessage, now)
		r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "ConvergenceDeadlineElapsed", message, now)
		r.setReady(cur, metav1.ConditionFalse, "ConvergenceDeadlineElapsed", message, now)
	}, reconcile.Result{RequeueAfter: time.Second})
}

func (r *Reconciler) verifyStandbyActivation(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	res *gnoi.OSVerifyResult,
	reachableMessage string,
	now time.Time,
) (reconcile.Result, error) {
	switch res.Standby.State {
	case gnoi.StandbyStateReady:
		if failure := strings.TrimSpace(res.Standby.ActivationFailMessage); failure != "" {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "StandbyActivationRejected",
				fmt.Sprintf("standby supervisor %s failed to activate: %s", res.Standby.ID, failure), now)
		}
		if versionMatches(res.Standby.Version, up.Spec.TargetVersion) {
			message := fmt.Sprintf("standby supervisor %s is ready on target version %s; activating the active supervisor",
				res.Standby.ID, res.Standby.Version)
			return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				cur.Status.Phase = opsv1alpha1.UpgradePhaseActivating
				cur.Status.RunningVersion = res.Version
				cur.Status.StandbySupervisorActivated = true
				cur.Status.Message = message
				cur.Status.FailureReason = ""
				r.setCondition(cur, conditionTypeMutationSettled, metav1.ConditionTrue, "StandbyActivationVerified", message, now)
				r.setCondition(cur, conditionTypeDeviceReachable, metav1.ConditionTrue, "DeviceReachable", reachableMessage, now)
				r.setCondition(cur, conditionTypeActivated, metav1.ConditionFalse, "StandbyVerified", message, now)
				r.setReady(cur, metav1.ConditionFalse, "StandbyVerified", message, now)
			}, reconcile.Result{RequeueAfter: time.Second})
		}
		return r.waitForStandbyActivation(ctx, up,
			fmt.Sprintf("standby supervisor %s is ready on %s while target is %s", res.Standby.ID, res.Standby.Version, up.Spec.TargetVersion), now)
	case gnoi.StandbyStateUnavailable:
		return r.waitForStandbyActivation(ctx, up, "standby supervisor is temporarily unavailable while rebooting", now)
	case gnoi.StandbyStateNotReported,
		gnoi.StandbyStateUnspecified,
		gnoi.StandbyStateUnsupported,
		gnoi.StandbyStateNonExistent,
		gnoi.StandbyStateUnknown:
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "StandbyVerificationUnavailable",
			fmt.Sprintf("OS.Verify requires individual-supervisor handling but reported standby state %s", res.Standby.State), now)
	default:
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "StandbyVerificationUnavailable",
			fmt.Sprintf("OS.Verify returned unrecognized standby state %q", res.Standby.State), now)
	}
}

func (r *Reconciler) waitForStandbyActivation(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	message string,
	now time.Time,
) (reconcile.Result, error) {
	if upgradeTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "StandbyActivationDidNotConverge", message, now)
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseAwaitingReachability
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeActivated, metav1.ConditionFalse, "StandbyActivationPending", message, now)
		r.setReady(cur, metav1.ConditionFalse, "StandbyActivationPending", message, now)
	}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
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
	if r.GNOI == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "NoGNOIProvider",
			"gNOI provider is required for final verification", now)
	}
	gnoiClient, err := r.gnoiClient(ctx)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		if reason, permanent := permanentGNOIError(err); permanent {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, reason, err.Error(), now)
		}
		return r.requeueAwaitingReachability(ctx, up, err, now)
	}
	res, err := verifyOS(ctx, gnoiClient)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		if reason, permanent := permanentGNOIError(err); permanent {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, reason, err.Error(), now)
		}
		return r.requeueAwaitingReachability(ctx, up, err, now)
	}
	requiresIndividual := up.Status.IndividualSupervisorInstall || res.IndividualSupervisorInstall
	if requiresIndividual && imageSourceStreamsBytes(up.Spec.ImageSource) &&
		(!up.Status.PrimarySupervisorInstalled || !up.Status.StandbySupervisorInstalled) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "SupervisorInstallIncomplete",
			"OS.Verify requires individual supervisor installs, but both supervisors were not durably validated before activation", now)
	}
	if requiresIndividual {
		if !up.Status.StandbySupervisorActivated || !primaryActivationRequestSubmitted(up) {
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "SupervisorActivationIncomplete",
				"individual-supervisor activation reached final verification without durable standby and active-supervisor milestones", now)
		}
		switch res.Standby.State {
		case gnoi.StandbyStateUnavailable:
			return r.waitForStandbyActivation(ctx, up, "standby supervisor is temporarily unavailable during final verification", now)
		case gnoi.StandbyStateReady:
			failure := strings.TrimSpace(res.Standby.ActivationFailMessage)
			if failure != "" || !versionMatches(res.Standby.Version, up.Spec.TargetVersion) {
				message := fmt.Sprintf("standby supervisor %s reports version %s; target is %s", res.Standby.ID, res.Standby.Version, up.Spec.TargetVersion)
				if failure != "" {
					message += ": " + failure
				}
				return r.waitForStandbyActivation(ctx, up, message, now)
			}
		case gnoi.StandbyStateNotReported,
			gnoi.StandbyStateUnspecified,
			gnoi.StandbyStateUnsupported,
			gnoi.StandbyStateNonExistent,
			gnoi.StandbyStateUnknown:
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "StandbyVerificationUnavailable",
				fmt.Sprintf("final OS.Verify requires the standby supervisor but reported state %s", res.Standby.State), now)
		default:
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "StandbyVerificationUnavailable",
				fmt.Sprintf("final OS.Verify returned unrecognized standby state %q", res.Standby.State), now)
		}
	}
	activationFailure := strings.TrimSpace(res.ActivationFailMessage)
	if !versionMatches(res.Version, up.Spec.TargetVersion) || activationFailure != "" {
		if requiresIndividual {
			message := fmt.Sprintf("active supervisor reports version %s; target is %s", res.Version, up.Spec.TargetVersion)
			if activationFailure != "" {
				message += ": " + activationFailure
			}
			return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "DualSupervisorVerificationFailed",
				message+"; automatic rollback is not attempted because it requires a separately verified per-supervisor rollback sequence", now)
		}
		if up.Status.PreviousVersion != "" && versionMatches(res.Version, up.Status.PreviousVersion) {
			message := fmt.Sprintf("device recovered on previous version %s after target activation failed", res.Version)
			if activationFailure != "" {
				message += ": " + activationFailure
			}
			return r.updateTerminalStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				cur.Status.Phase = opsv1alpha1.UpgradePhaseRolledBack
				cur.Status.RunningVersion = res.Version
				cur.Status.FailureReason = "RolledBack"
				cur.Status.CompletionTime = &metav1.Time{Time: now}
				cur.Status.Message = message
				r.setCondition(cur, conditionTypeRollback, metav1.ConditionTrue, "RolledBack", message, now)
				r.setReady(cur, metav1.ConditionFalse, "RolledBack", message, now)
			}, reconcile.Result{})
		}
		rollback := up.Spec.RollbackOnFailure == nil || *up.Spec.RollbackOnFailure
		if rollback {
			if up.Status.PreviousVersion == "" {
				return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "RollbackVersionMissing",
					fmt.Sprintf("device runs %s but spec targets %s; rollbackOnFailure requested, but no previous version was captured",
						res.Version, up.Spec.TargetVersion), now)
			}
			if r.Lifecycle == nil {
				return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "RollbackInventoryUnavailable",
					"rollback requested, but no lifecycle backend can prove the previous version remains activatable", now)
			}
			previous, inspectErr := r.inspectTarget(ctx, up.Status.PreviousVersion)
			if inspectErr != nil || !previous.State.Activatable() {
				message := fmt.Sprintf("rollback target %s is not proven activatable", up.Status.PreviousVersion)
				if inspectErr != nil {
					message += ": " + inspectErr.Error()
				}
				return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "RollbackTargetUnavailable", message, now)
			}
			return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				cur.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
				cur.Status.RunningVersion = res.Version
				cur.Status.ValidatedVersion = previous.Version
				if cur.Status.RollbackStartTime == nil {
					cur.Status.RollbackStartTime = &metav1.Time{Time: now}
				}
				cur.Status.FailureReason = ""
				cur.Status.Message = fmt.Sprintf("device runs %s but target is %s; rolling back to previous version %s",
					res.Version, up.Spec.TargetVersion, cur.Status.PreviousVersion)
				r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "VerifyMismatch", cur.Status.Message, now)
				r.setCondition(cur, conditionTypeRollback, metav1.ConditionFalse, "RollbackPending",
					fmt.Sprintf("waiting to activate previous version %s", cur.Status.PreviousVersion), now)
				r.setReady(cur, metav1.ConditionFalse, "RollbackPending", cur.Status.Message, now)
			}, reconcile.Result{RequeueAfter: time.Second})
		}
		return r.updateTerminalStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			cur.Status.Phase = opsv1alpha1.UpgradePhaseFailed
			cur.Status.RunningVersion = res.Version
			cur.Status.FailureReason = "VerifyMismatch"
			cur.Status.CompletionTime = &metav1.Time{Time: now}
			cur.Status.Message = fmt.Sprintf("device runs %s; target was %s", res.Version, up.Spec.TargetVersion)
			if activationFailure != "" {
				cur.Status.Message += ": " + activationFailure
			}
			r.setCondition(cur, conditionTypeVerified, metav1.ConditionFalse, "VerifyMismatch", cur.Status.Message, now)
			r.setReady(cur, metav1.ConditionFalse, "VerifyMismatch", cur.Status.Message, now)
		}, reconcile.Result{})
	}
	if ready, detail := allRequiredSupervisorsRunTarget(res, up.Spec.TargetVersion, requiresIndividual); !ready {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseValidationFailed, "SupervisorTargetMismatch", detail, now)
	}
	return r.updateTerminalStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
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

func (r *Reconciler) runRollingBack(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	previous := up.Status.PreviousVersion
	if previous == "" {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "RollbackVersionMissing",
			"rollback requested, but no previous version was captured before activation", now)
	}
	// Normal verification records this timestamp when it enters RollingBack.
	// Persist it here as well for objects created by an older controller or
	// recovered from incomplete status, so pre-dispatch reachability waits are
	// always bounded by an independent rollback deadline.
	if rollbackWaitStart(up) == nil {
		return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
			if cur.Status.RollbackStartTime == nil {
				cur.Status.RollbackStartTime = &metav1.Time{Time: now}
			}
			cur.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
			cur.Status.Message = fmt.Sprintf("rollback deadline recorded; preparing to activate previous version %s", previous)
			cur.Status.FailureReason = ""
			r.setCondition(cur, conditionTypeRollback, metav1.ConditionFalse, "RollbackPending", cur.Status.Message, now)
			r.setReady(cur, metav1.ConditionFalse, "RollbackPending", cur.Status.Message, now)
		}, reconcile.Result{RequeueAfter: time.Second})
	}
	if rollbackTimedOut(up, now) {
		if rollbackRequestSubmitted(up) {
			if remaining := rollbackResultGraceRemaining(up, now); remaining > 0 {
				return reconcile.Result{RequeueAfter: boundedDuration(time.Second, remaining)}, nil
			}
		}
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "RollbackDidNotConverge",
			fmt.Sprintf("rollback to %s did not converge within %s", previous, rebootTimeout(up)), now)
	}
	if r.GNOI == nil {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "NoGNOIProvider",
			"gNOI provider is required for rollback", now)
	}
	gnoiClient, err := r.gnoiClient(ctx)
	if err != nil {
		r.resetGNOIClientIfTransient(ctx, err)
		return r.requeueRollback(ctx, up, fmt.Errorf("waiting for device before rollback: %w", err), now)
	}
	now = r.now()
	if rollbackTimedOut(up, now) {
		if rollbackRequestSubmitted(up) {
			if remaining := rollbackResultGraceRemaining(up, now); remaining > 0 {
				return reconcile.Result{RequeueAfter: boundedDuration(time.Second, remaining)}, nil
			}
		}
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "RollbackDidNotConverge",
			fmt.Sprintf("rollback to %s did not converge within %s", previous, rebootTimeout(up)), now)
	}

	if rollbackRequestSubmitted(up) {
		res, err := verifyOS(ctx, gnoiClient)
		if err != nil {
			r.resetGNOIClientIfTransient(ctx, err)
			return r.requeueRollback(ctx, up, fmt.Errorf("waiting for device after rollback activation: %w", err), now)
		}
		if versionMatches(res.Version, previous) {
			return r.updateTerminalStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
				cur.Status.Phase = opsv1alpha1.UpgradePhaseRolledBack
				cur.Status.RunningVersion = res.Version
				cur.Status.FailureReason = "RolledBack"
				cur.Status.CompletionTime = &metav1.Time{Time: now}
				cur.Status.Message = fmt.Sprintf("rollback complete; device is running previous version %s", res.Version)
				r.setCondition(cur, conditionTypeRollback, metav1.ConditionTrue, "RolledBack", cur.Status.Message, now)
				r.setReady(cur, metav1.ConditionFalse, "RolledBack", cur.Status.Message, now)
			}, reconcile.Result{})
		}
		return r.requeueRollback(ctx, up,
			fmt.Errorf("device is reachable on %s while rollback target is %s", res.Version, previous), now)
	}
	if maintenanceWindowExpired(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "MaintenanceWindowExpired",
			"maintenance window closed before rollback activation could be submitted", now)
	}

	rollbackVersion := previous
	if up.Status.ValidatedVersion != "" && versionMatches(up.Status.ValidatedVersion, previous) {
		rollbackVersion = up.Status.ValidatedVersion
	}
	message := fmt.Sprintf("submitting gNOI OS.Activate rollback to exact previous version %s", rollbackVersion)
	claimed, deleting, err := r.claimRollbackActivation(ctx, up, rollbackVersion, message, now)
	if err != nil {
		return reconcile.Result{}, err
	}
	if deleting != nil {
		return r.handleDelete(ctx, deleting, now)
	}
	if !claimed {
		return reconcile.Result{RequeueAfter: time.Second}, nil
	}
	r.emitEvent(up, corev1.EventTypeWarning, "RollbackRequested", message)
	activateCtx, cancel := context.WithTimeout(ctx,
		boundedDuration(activationRPCTimeout, remainingRollbackTime(up, now)))
	log.G(ctx).WithField("softwareUpgrade", client.ObjectKeyFromObject(up).String()).
		WithField("version", rollbackVersion).
		Warn("dispatching gNOI OS.Activate rollback")
	activateErr := gnoiClient.Activate(activateCtx, gnoi.ActivateOpts{Version: rollbackVersion})
	cancel()
	now = r.now()
	if activateErr != nil && !activationMayHaveStarted(activateErr) {
		r.resetGNOIClientIfTransient(ctx, activateErr)
		return r.terminalAfterMutation(ctx, up, opsv1alpha1.UpgradePhaseFailed, "RollbackActivateFailed", activateErr.Error(), now)
	}
	if activateErr != nil {
		r.resetGNOIClientIfTransient(ctx, activateErr)
		message = fmt.Sprintf("%s; device connection closed during rollback activation: %s", message, activateErr.Error())
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Message = message
		r.setCondition(cur, conditionTypeRollback, metav1.ConditionFalse, "RollbackDispatched", message, now)
		r.setReady(cur, metav1.ConditionFalse, "RollbackDispatched", message, now)
	}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
}

func (r *Reconciler) requeueRollback(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, err error, now time.Time) (reconcile.Result, error) {
	timeoutSec := up.Spec.RebootTimeoutSeconds
	if timeoutSec == 0 {
		timeoutSec = 1800
	}
	if rollbackTimedOut(up, now) {
		return r.terminal(ctx, up, opsv1alpha1.UpgradePhaseFailed, "RollbackDidNotConverge",
			fmt.Sprintf("rollback to %s did not converge within %ds: %s", up.Status.PreviousVersion, timeoutSec, err.Error()), now)
	}
	message := err.Error()
	reason := "RollbackWaiting"
	if rollbackRequestSubmitted(up) {
		reason = "RollbackDispatched"
	}
	return r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		r.setCondition(cur, conditionTypeRollback, metav1.ConditionFalse, reason, message, now)
		r.setReady(cur, metav1.ConditionFalse, reason, message, now)
	}, reconcile.Result{RequeueAfter: awaitingReachabilityPoll})
}

func upgradeTimedOut(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) bool {
	since := upgradeWaitStart(up)
	return since != nil && now.Sub(*since) >= rebootTimeout(up)
}

func activationControlTimedOut(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) bool {
	return upgradeWaitStart(up) == nil && up.Status.ActivationControlStartTime != nil &&
		now.Sub(up.Status.ActivationControlStartTime.Time) >= rebootTimeout(up)
}

func rollbackTimedOut(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) bool {
	since := rollbackWaitStart(up)
	return since != nil && now.Sub(*since) >= rebootTimeout(up)
}

func rebootTimeout(up *opsv1alpha1.IOSXESoftwareUpgrade) time.Duration {
	seconds := up.Spec.RebootTimeoutSeconds
	if seconds == 0 {
		seconds = 1800
	}
	return time.Duration(seconds) * time.Second
}

func remainingActivationTime(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) time.Duration {
	return remainingOperationTime(upgradeWaitStart(up), rebootTimeout(up), now)
}

func activationRPCGraceRemaining(up *opsv1alpha1.IOSXESoftwareUpgrade, standby bool, now time.Time) time.Duration {
	requested := up.Status.PrimarySupervisorActivationRequested
	reason := "ActivationRequested"
	if standby {
		requested = up.Status.StandbySupervisorActivationRequested
		reason = "StandbyActivationRequested"
	}
	var requestStart *time.Time
	if requested {
		for i := range up.Status.Conditions {
			condition := &up.Status.Conditions[i]
			if condition.Type == conditionTypeActivated && condition.Reason == reason {
				requestStart = &condition.LastTransitionTime.Time
				break
			}
		}
	}
	if requestStart == nil && up.Status.ActivationStartTime != nil {
		requestStart = &up.Status.ActivationStartTime.Time
	}
	if requestStart == nil {
		return 0
	}
	deadline := requestStart.Add(boundedDuration(activationRPCTimeout, rebootTimeout(up)))
	if up.Status.ActivationStartTime != nil {
		sequenceDeadline := up.Status.ActivationStartTime.Time.Add(rebootTimeout(up))
		if sequenceDeadline.Before(deadline) {
			deadline = sequenceDeadline
		}
	}
	remaining := deadline.Add(mutationResultGrace).Sub(now)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func stagingRPCGraceRemaining(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) time.Duration {
	if up.Status.InstallStartTime == nil {
		return 0
	}
	deadline := up.Status.InstallStartTime.Time.Add(boundedDuration(lifecycleMutationTimeout, installTimeout(up)))
	remaining := deadline.Add(mutationResultGrace).Sub(now)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func remainingRollbackTime(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) time.Duration {
	return remainingOperationTime(rollbackWaitStart(up), rebootTimeout(up), now)
}

func installResultGraceRemaining(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) time.Duration {
	start := up.Status.InstallStartTime
	if start == nil {
		start = up.Status.StartTime
	}
	if start == nil {
		return 0
	}
	remaining := start.Time.Add(installTimeout(up) + mutationResultGrace).Sub(now)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func rollbackResultGraceRemaining(up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) time.Duration {
	start := rollbackWaitStart(up)
	if start == nil {
		return 0
	}
	remaining := start.Add(rebootTimeout(up) + mutationResultGrace).Sub(now)
	if remaining <= 0 {
		return 0
	}
	return remaining
}

func remainingOperationTime(start *time.Time, limit time.Duration, now time.Time) time.Duration {
	if start == nil {
		return limit
	}
	remaining := limit - now.Sub(*start)
	if remaining <= 0 {
		return time.Nanosecond
	}
	return remaining
}

func boundedDuration(limit, remaining time.Duration) time.Duration {
	if remaining <= 0 {
		return time.Nanosecond
	}
	if remaining < limit {
		return remaining
	}
	return limit
}

func rollbackWaitStart(up *opsv1alpha1.IOSXESoftwareUpgrade) *time.Time {
	if up.Status.RollbackStartTime != nil {
		return &up.Status.RollbackStartTime.Time
	}
	for _, cond := range up.Status.Conditions {
		if cond.Type == conditionTypeRollback &&
			(cond.Reason == "RollbackPending" || cond.Reason == "RollbackRequested" || cond.Reason == "RollbackDispatched") {
			return &cond.LastTransitionTime.Time
		}
	}
	return nil
}

func rollbackRequestSubmitted(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	if up.Status.RollbackActivationRequested {
		return true
	}
	for _, cond := range up.Status.Conditions {
		if cond.Type == conditionTypeRollback &&
			(cond.Reason == "RollbackRequested" || cond.Reason == "RollbackDispatched") {
			return true
		}
	}
	return false
}

func (r *Reconciler) claimRollbackActivation(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	version string,
	message string,
	now time.Time,
) (bool, *opsv1alpha1.IOSXESoftwareUpgrade, error) {
	claimed := false
	var deleting *opsv1alpha1.IOSXESoftwareUpgrade
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXESoftwareUpgrade
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(up), &cur); err != nil {
			if apierrors.IsNotFound(err) {
				deleting = missingUpgradeCleanupCandidate(up, now)
				return nil
			}
			return err
		}
		if !cur.DeletionTimestamp.IsZero() {
			deleting = cur.DeepCopy()
			return nil
		}
		if !upgradeStatusCASMatches(up, &cur) {
			return nil
		}
		if rollbackRequestSubmitted(&cur) || terminalUpgradePhase(cur.Status.Phase) {
			return nil
		}
		if cur.Status.RollbackStartTime == nil {
			cur.Status.RollbackStartTime = &metav1.Time{Time: now}
		}
		cur.Status.RollbackActivationRequested = true
		cur.Status.Phase = opsv1alpha1.UpgradePhaseRollingBack
		cur.Status.ValidatedVersion = version
		cur.Status.Message = message
		cur.Status.FailureReason = ""
		cur.Status.ObservedGeneration = cur.Generation
		r.setCondition(&cur, conditionTypeMutationSettled, metav1.ConditionFalse, "MutationRequested", message, now)
		r.setCondition(&cur, conditionTypeRollback, metav1.ConditionFalse, "RollbackRequested", message, now)
		r.setReady(&cur, metav1.ConditionFalse, "RollbackRequested", message, now)
		if err := r.Client.Status().Update(ctx, &cur); err != nil {
			return err
		}
		up.Status = *cur.Status.DeepCopy()
		claimed = true
		return nil
	})
	if err != nil {
		return false, nil, fmt.Errorf("claim rollback activation request: %w", err)
	}
	return claimed, deleting, nil
}

func missingUpgradeCleanupCandidate(
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	now time.Time,
) *opsv1alpha1.IOSXESoftwareUpgrade {
	if up == nil || upgradeMutationSubmitted(up) {
		return nil
	}
	missing := up.DeepCopy()
	missing.DeletionTimestamp = &metav1.Time{Time: now}
	return missing
}

func rememberPreviousVersion(up *opsv1alpha1.IOSXESoftwareUpgrade, previous string) {
	if up.Status.PreviousVersion == "" && previous != "" {
		up.Status.PreviousVersion = previous
	}
}

func (r *Reconciler) gnoiClient(ctx context.Context) (*gnoi.Client, error) {
	if r.GNOI == nil {
		return nil, errors.New("gNOI provider is not configured")
	}
	callCtx, cancel := context.WithTimeout(ctx, controlRPCTimeout)
	defer cancel()
	return r.GNOI.GNOIClient(callCtx)
}

func verifyOS(ctx context.Context, client *gnoi.Client) (*gnoi.OSVerifyResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, controlRPCTimeout)
	defer cancel()
	return client.Verify(callCtx)
}

func deviceTime(ctx context.Context, client *gnoi.Client) (time.Time, error) {
	callCtx, cancel := context.WithTimeout(ctx, controlRPCTimeout)
	defer cancel()
	return client.Time(callCtx)
}

func (r *Reconciler) resetGNOIClientIfTransient(ctx context.Context, err error) {
	if err == nil || !isTransientGNOIError(err) {
		return
	}
	resetter, ok := r.GNOI.(gnoi.ResetProvider)
	if !ok {
		return
	}
	resetter.ResetGNOIClient(ctx)
}

func upgradeWaitStart(up *opsv1alpha1.IOSXESoftwareUpgrade) *time.Time {
	if up.Status.ActivationStartTime != nil {
		return &up.Status.ActivationStartTime.Time
	}
	for _, cond := range up.Status.Conditions {
		if cond.Type == conditionTypeActivated && cond.Status == metav1.ConditionTrue {
			return &cond.LastTransitionTime.Time
		}
	}
	return nil
}

func (r *Reconciler) handleDelete(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, now time.Time) (reconcile.Result, error) {
	// There is no safe device-side cancel operation. Never dispatch a mutation
	// from the deletion path; release the Lease only if no mutation was claimed.
	quarantineRequired := upgradeStateRequiresQuarantine(up, now)
	if quarantineRequired {
		result := reconcile.Result{RequeueAfter: installInventoryPoll}
		owned, holder, _, err := r.acquireQuarantineLease(ctx, up, true, now)
		if err != nil {
			return result, fmt.Errorf("acquire software-upgrade deletion quarantine lease: %w", err)
		}
		if !owned {
			log.G(ctx).WithField("softwareUpgrade", client.ObjectKeyFromObject(up).String()).
				WithField("leaseHolder", holder).
				Warn("deletion is waiting for the shared mutation quarantine lease")
			return result, nil
		}
		reason := "LegacyStateOutcomeUnknown"
		message := fmt.Sprintf("deleting unmarked upgrade in phase %s while retaining its device mutation quarantine", up.Status.Phase)
		if unsupportedExecutionModel(up) {
			reason = "UnsupportedExecutionModel"
			message = fmt.Sprintf("deleting upgrade with unsupported execution model %q while retaining its device mutation quarantine", up.Status.ExecutionModel)
		}
		r.emitEvent(up, corev1.EventTypeWarning, reason, message)
	}
	switch up.Status.Phase {
	case opsv1alpha1.UpgradePhaseStaging,
		opsv1alpha1.UpgradePhaseTransferring,
		opsv1alpha1.UpgradePhaseTransferInterrupted,
		opsv1alpha1.UpgradePhaseValidating,
		opsv1alpha1.UpgradePhaseActivating,
		opsv1alpha1.UpgradePhaseAwaitingReachability,
		opsv1alpha1.UpgradePhaseVerifying,
		opsv1alpha1.UpgradePhaseRollingBack:
		if r.Recorder != nil {
			r.Recorder.Eventf(up, "Warning", "DeleteDuringInflightUpgrade",
				"deletion requested in phase %s; device-side activate may complete asynchronously", up.Status.Phase)
		}
	}
	if !quarantineRequired &&
		((terminalUpgradePhase(up.Status.Phase) && !retainMutationLeaseUntilExpiry(up)) || !upgradeMutationSubmitted(up)) {
		if err := r.releaseMutationLease(ctx, up); err != nil {
			return reconcile.Result{}, err
		}
	}
	if controllerutil.ContainsFinalizer(up, upgradeFinalizer) {
		controllerutil.RemoveFinalizer(up, upgradeFinalizer)
		if err := r.Client.Update(ctx, up); err != nil && !apierrors.IsNotFound(err) {
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
	return r.terminalWithEvidence(ctx, up, phase, reason, message, now, false)
}

// terminalAfterMutation is only for a definitive mutation response or a
// correlated completion observation, never an error from a read-only RPC.
func (r *Reconciler) terminalAfterMutation(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, phase opsv1alpha1.UpgradePhase, reason, message string, now time.Time) (reconcile.Result, error) {
	return r.terminalWithEvidence(ctx, up, phase, reason, message, now, true)
}

func (r *Reconciler) terminalWithEvidence(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, phase opsv1alpha1.UpgradePhase, reason, message string, now time.Time, settled bool) (reconcile.Result, error) {
	return r.updateTerminalStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Phase = phase
		cur.Status.FailureReason = reason
		cur.Status.Message = message
		cur.Status.CompletionTime = &metav1.Time{Time: now}
		if settled {
			r.setCondition(cur, conditionTypeMutationSettled, metav1.ConditionTrue, "MutationCompleted", message, now)
		}
		condStatus := metav1.ConditionFalse
		if phase == opsv1alpha1.UpgradePhaseSucceeded {
			condStatus = metav1.ConditionTrue
			markTransferComplete(cur)
		}
		r.setReady(cur, condStatus, reason, message, now)
	}, reconcile.Result{})
}

func (r *Reconciler) updateTerminalStatus(
	ctx context.Context,
	up *opsv1alpha1.IOSXESoftwareUpgrade,
	mutate func(*opsv1alpha1.IOSXESoftwareUpgrade),
	result reconcile.Result,
) (reconcile.Result, error) {
	updatedResult, err := r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		mutate(cur)
		if successfulUpgradeOutcome(cur.Status.Phase) {
			r.setCondition(cur, conditionTypeMutationSettled, metav1.ConditionTrue, "OutcomeVerified", cur.Status.Message, r.now())
		}
	}, result)
	if err != nil {
		return updatedResult, err
	}
	var current opsv1alpha1.IOSXESoftwareUpgrade
	reader := r.Reader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(up), &current); err != nil {
		return updatedResult, fmt.Errorf("read terminal upgrade before lease release: %w", err)
	}
	if !terminalUpgradePhase(current.Status.Phase) {
		// A concurrent reconciler advanced the phase first, so updateStatus
		// correctly rejected our stale terminal closure. Re-evaluate that state
		// without releasing its mutation lease.
		return reconcile.Result{RequeueAfter: time.Second}, nil
	}
	if retainMutationLeaseUntilExpiry(&current) {
		return updatedResult, nil
	}
	// Release immediately after persisting a terminal outcome. Waiting for a
	// follow-up reconcile leaves a deletion race that can quarantine the device
	// behind the long-running mutation lease until its TTL expires.
	if err := r.releaseMutationLease(ctx, up); err != nil {
		return updatedResult, err
	}
	return updatedResult, nil
}

func retainMutationLeaseUntilExpiry(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	if up == nil {
		return false
	}
	if unsupportedExecutionModel(up) {
		return true
	}
	if up.Status.FailureReason == "InstallAttemptMarkerMissing" ||
		up.Status.FailureReason == "StagingOperationMissing" ||
		up.Status.FailureReason == "LegacyStateOutcomeUnknown" {
		return true
	}
	if !upgradeMutationSubmitted(up) {
		return false
	}
	// Successful legacy terminals predate the explicit evidence condition but
	// already encode a verified outcome. Otherwise absence of evidence is an
	// unknown outcome, including permanent failures of observation/control RPCs.
	return !successfulUpgradeOutcome(up.Status.Phase) &&
		!apimeta.IsStatusConditionTrue(up.Status.Conditions, conditionTypeMutationSettled)
}

func successfulUpgradeOutcome(phase opsv1alpha1.UpgradePhase) bool {
	return phase == opsv1alpha1.UpgradePhaseSucceeded ||
		phase == opsv1alpha1.UpgradePhaseStagedForNextBoot ||
		phase == opsv1alpha1.UpgradePhaseRolledBack
}

func unsupportedExecutionModel(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	return up != nil && up.Status.ExecutionModel != "" &&
		up.Status.ExecutionModel != opsv1alpha1.UpgradeExecutionModelAtMostOnceV1
}

func markTransferComplete(up *opsv1alpha1.IOSXESoftwareUpgrade) {
	if up.Status.TransferProgress == nil || up.Status.TransferProgress.TotalBytes <= 0 {
		return
	}
	up.Status.TransferProgress.BytesTransferred = up.Status.TransferProgress.TotalBytes
	up.Status.TransferProgress.Percent = 100
}

func (r *Reconciler) updateTransferProgress(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, bytesReceived uint64, total int64, now time.Time) {
	// Throttle small increments and reject non-monotonic device counters.
	if up.Status.TransferProgress != nil && up.Status.TransferProgress.BytesTransferred > 0 {
		last := uint64(up.Status.TransferProgress.BytesTransferred)
		if bytesReceived <= last || bytesReceived-last < 64*1024 {
			return
		}
	}
	if total > 0 && bytesReceived > uint64(total) {
		bytesReceived = uint64(total)
	}
	percent := int32(0)
	if total > 0 {
		percent = int32(bytesReceived * 100 / uint64(total))
		if percent > 100 {
			percent = 100
		}
	}
	progress := &opsv1alpha1.UpgradeTransferProgress{
		BytesTransferred: int64(bytesReceived),
		TotalBytes:       total,
		Percent:          percent,
	}
	updated := false
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXESoftwareUpgrade
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(up), &cur); err != nil {
			return err
		}
		if cur.Generation != up.Generation || cur.Status.Phase != up.Status.Phase || terminalUpgradePhase(cur.Status.Phase) {
			return nil
		}
		if existing := cur.Status.TransferProgress; existing != nil && existing.BytesTransferred > 0 {
			last := uint64(existing.BytesTransferred)
			if bytesReceived <= last || bytesReceived-last < 64*1024 {
				return nil
			}
		}
		cur.Status.TransferProgress = progress.DeepCopy()
		cur.Status.Message = fmt.Sprintf("transferring: %d/%d bytes (%d%%)", bytesReceived, total, percent)
		cur.Status.ObservedGeneration = cur.Generation
		if err := r.Client.Status().Update(ctx, &cur); err != nil {
			return err
		}
		updated = true
		return nil
	})
	if err != nil {
		log.G(ctx).WithError(err).Warn("IOSXESoftwareUpgrade transfer progress status update failed")
	} else if updated {
		up.Status.TransferProgress = progress
	}
}

func (r *Reconciler) updateSyncProgress(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, percent uint32, standby bool, now time.Time) {
	if percent > 100 {
		percent = 100
	}
	supervisor := "primary"
	if standby {
		supervisor = "standby"
	}
	message := fmt.Sprintf("synchronizing image to %s supervisor: %d%%", supervisor, percent)
	if _, err := r.updateStatus(ctx, up, func(cur *opsv1alpha1.IOSXESoftwareUpgrade) {
		cur.Status.Message = message
		r.setCondition(cur, conditionTypeTransferred, metav1.ConditionFalse, "SupervisorSync", message, now)
		r.setReady(cur, metav1.ConditionFalse, "SupervisorSync", message, now)
	}, reconcile.Result{}); err != nil {
		log.G(ctx).WithError(err).Warn("IOSXESoftwareUpgrade supervisor sync status update failed")
	}
}

func (r *Reconciler) setReady(up *opsv1alpha1.IOSXESoftwareUpgrade, status metav1.ConditionStatus, reason, message string, now time.Time) {
	r.setCondition(up, conditionTypeReady, status, reason, message, now)
}

func (r *Reconciler) setCondition(up *opsv1alpha1.IOSXESoftwareUpgrade, condType string, status metav1.ConditionStatus, reason, message string, now time.Time) {
	apimeta.SetStatusCondition(&up.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Time{Time: now},
		ObservedGeneration: up.Generation,
	})
}

// upgradeStatusCASMatches includes durable mutation claims, immutable content
// and version bindings, monotonic completion milestones, and the install
// deadline anchor in the compare-and-swap token. Phase alone is insufficient
// because several of these fields advance without a phase transition.
// Inventory/running-version observations, transfer progress, messages, and
// conditions are deliberately excluded because they may change within a phase.
func upgradeStatusCASMatches(expected, current *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	if expected == nil || current == nil || expected.Generation != current.Generation ||
		expected.Status.Phase != current.Status.Phase ||
		expected.Status.ExecutionModel != current.Status.ExecutionModel {
		return false
	}
	e, c := expected.Status, current.Status
	return e.SourceDigest == c.SourceDigest &&
		e.SourceSize == c.SourceSize &&
		e.StagingOperationID == c.StagingOperationID &&
		e.StagingRequested == c.StagingRequested &&
		e.PreviousVersion == c.PreviousVersion &&
		e.ValidatedVersion == c.ValidatedVersion &&
		e.IndividualSupervisorInstall == c.IndividualSupervisorInstall &&
		e.PrimarySupervisorInstalled == c.PrimarySupervisorInstalled &&
		e.PrimarySupervisorInstallRequested == c.PrimarySupervisorInstallRequested &&
		e.StandbySupervisorInstalled == c.StandbySupervisorInstalled &&
		e.StandbySupervisorInstallRequested == c.StandbySupervisorInstallRequested &&
		e.PrimarySupervisorActivationRequested == c.PrimarySupervisorActivationRequested &&
		e.StandbySupervisorActivationRequested == c.StandbySupervisorActivationRequested &&
		e.StandbySupervisorActivated == c.StandbySupervisorActivated &&
		e.RollbackActivationRequested == c.RollbackActivationRequested &&
		e.NoRebootActivationAccepted == c.NoRebootActivationAccepted &&
		(e.InstallStartTime == nil) == (c.InstallStartTime == nil) &&
		(e.ActivationControlStartTime == nil) == (c.ActivationControlStartTime == nil)
}

func (r *Reconciler) updateStatus(ctx context.Context, up *opsv1alpha1.IOSXESoftwareUpgrade, mutate func(*opsv1alpha1.IOSXESoftwareUpgrade), result reconcile.Result) (reconcile.Result, error) {
	var beforePhase, afterPhase opsv1alpha1.UpgradePhase
	var afterReason, afterMessage string
	updated := false
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
		afterPhase = cur.Status.Phase
		afterMessage = cur.Status.Message
		afterReason = readyReason(cur.Status.Conditions)
		// The phase and durable mutation fields read at the start of this
		// reconcile are our compare-and-swap token. Another replica may
		// legitimately hold the same CR-scoped Lease; never let its stale closure
		// regress a newer claim, phase, or terminal record.
		if !upgradeStatusCASMatches(up, &cur) || terminalUpgradePhase(cur.Status.Phase) {
			return nil
		}
		mutate(&cur)
		cur.Status.ObservedGeneration = cur.Generation
		afterPhase = cur.Status.Phase
		afterMessage = cur.Status.Message
		afterReason = readyReason(cur.Status.Conditions)
		if err := r.Client.Status().Update(ctx, &cur); err != nil {
			return err
		}
		updated = true
		return nil
	})
	if err != nil {
		return result, fmt.Errorf("update upgrade status: %w", err)
	}
	setSoftwareUpgradeSpanOutcome(oteltrace.SpanFromContext(ctx), afterPhase, afterReason, afterMessage)
	if updated && afterPhase != "" && afterPhase != beforePhase {
		eventType := corev1.EventTypeNormal
		if isTerminalFailurePhase(afterPhase) {
			eventType = corev1.EventTypeWarning
		}
		if afterReason == "" {
			afterReason = string(afterPhase)
		}
		log.G(ctx).WithField("softwareUpgrade", client.ObjectKeyFromObject(up).String()).
			WithField("from", beforePhase).
			WithField("to", afterPhase).
			WithField("reason", afterReason).
			Info("IOSXESoftwareUpgrade phase advanced")
		recordPhaseTransition(r.DeviceName, up.Spec.TargetVersion, string(beforePhase), string(afterPhase), afterReason)
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
	if src.Preinstalled != nil {
		count++
	}
	if src.DeviceFile != nil {
		count++
	}
	if src.LocalPath != "" {
		count++
	}
	if count == 0 {
		return errors.New("imageSource: exactly one of url, configMapRef, preinstalled, deviceFile, or localPath is required")
	}
	if count > 1 {
		return errors.New("imageSource: only one of url, configMapRef, preinstalled, deviceFile, or localPath may be set")
	}
	if (src.URL == "") != (src.SHA256 == "") {
		return errors.New("imageSource.sha256 must be set if and only if imageSource.url is set")
	}
	if src.URL != "" {
		parsedURL, err := url.Parse(src.URL)
		if err != nil {
			return errors.New("imageSource.url must be a valid URL")
		}
		if parsedURL.User != nil {
			return errors.New("imageSource.url must not contain user information; use an endpoint-bound urlSecretRef")
		}
	}
	if src.URLSecretRef != nil && src.URL == "" {
		return errors.New("imageSource.urlSecretRef may be set only when imageSource.url is set")
	}
	if src.URLSecretRef != nil && strings.TrimSpace(src.URLSecretRef.Name) == "" {
		return errors.New("imageSource.urlSecretRef.name must not be empty")
	}
	if src.URLSecretRef != nil &&
		!strings.HasPrefix(src.URL, "ftp://") &&
		!strings.HasPrefix(src.URL, "scp://") &&
		!strings.HasPrefix(src.URL, "sftp://") {
		return errors.New("imageSource.urlSecretRef is supported only for ftp, scp, or sftp URLs")
	}
	if src.ConfigMapRef != nil && strings.TrimSpace(src.ConfigMapRef.Name) == "" {
		return errors.New("imageSource.configMapRef.name must not be empty")
	}
	if src.LocalPathSHA256 != "" && src.LocalPath == "" {
		return errors.New("imageSource.localPathSHA256 may be set only when imageSource.localPath is set")
	}
	if src.DeviceFile != nil {
		if strings.TrimSpace(src.DeviceFile.Path) == "" {
			return errors.New("imageSource.deviceFile.path must not be empty")
		}
		if !validSHA256Hex(src.DeviceFile.SHA256) {
			return errors.New("imageSource.deviceFile.sha256 must be 64 lowercase hexadecimal characters")
		}
	}
	if src.SHA256 != "" && !validSHA256Hex(src.SHA256) {
		return errors.New("imageSource.sha256 must be 64 lowercase hexadecimal characters")
	}
	if src.LocalPathSHA256 != "" && !validSHA256Hex(src.LocalPathSHA256) {
		return errors.New("imageSource.localPathSHA256 must be 64 lowercase hexadecimal characters")
	}
	return nil
}

func activationMayHaveStarted(err error) bool {
	if err == nil {
		return false
	}
	var activateErr *gnoi.ActivateError
	if errors.As(err, &activateErr) {
		return false
	}
	if _, definitiveRejection := permanentGNOIError(err); definitiveRejection {
		return false
	}
	// After the durable claim, every unclassified transport, protocol, or
	// response-shape error is indeterminate. A denylist is unsafe because new
	// gRPC status codes or malformed success responses may arrive after IOS XE
	// accepted the mutation.
	return true
}

func isTransientGNOIError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, io.EOF) {
		return true
	}
	switch status.Code(err) {
	case codes.DeadlineExceeded, codes.Canceled, codes.Unavailable, codes.ResourceExhausted, codes.Aborted:
		return true
	default:
		return false
	}
}

func primaryActivationRequestSubmitted(up *opsv1alpha1.IOSXESoftwareUpgrade) bool {
	if up.Status.PrimarySupervisorActivationRequested {
		return true
	}
	for _, cond := range up.Status.Conditions {
		if cond.Type == conditionTypeActivated && cond.Reason == "ActivationRequested" {
			return true
		}
	}
	return false
}

func validSHA256Hex(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func validContentDigest(value string) bool {
	digest, ok := strings.CutPrefix(value, "sha256:")
	return ok && validSHA256Hex(digest)
}

func allRequiredSupervisorsRunTarget(res *gnoi.OSVerifyResult, target string, requireIndividual bool) (bool, string) {
	if res == nil {
		return false, "OS.Verify returned no result"
	}
	if failure := strings.TrimSpace(res.ActivationFailMessage); failure != "" {
		return false, fmt.Sprintf("active supervisor reports activation failure: %s", failure)
	}
	if !versionMatches(res.Version, target) {
		return false, fmt.Sprintf("active supervisor reports version %s; target is %s", res.Version, target)
	}
	if !requireIndividual {
		return true, ""
	}
	if res.Standby.State != gnoi.StandbyStateReady {
		return false, fmt.Sprintf("individual-supervisor verification reports standby state %s", res.Standby.State)
	}
	if failure := strings.TrimSpace(res.Standby.ActivationFailMessage); failure != "" {
		return false, fmt.Sprintf("standby supervisor %s reports activation failure: %s", res.Standby.ID, failure)
	}
	if !versionMatches(res.Standby.Version, target) {
		return false, fmt.Sprintf("standby supervisor %s reports version %s; target is %s", res.Standby.ID, res.Standby.Version, target)
	}
	return true, ""
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
