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
// the write-class gNOI operations (Reboot, FactoryReset, FilePut,
// FileRemove, KillProcess, CancelReboot). Distinct from the read-only
// DeviceOperation reconciler so RBAC can grant the two surfaces
// independently.
package operationalaction

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
)

const (
	conditionTypeReady = "Ready"
	finalizerName      = "ops.cisco.vk/iosxeoperationalaction-finalizer"
)

// Reconciler executes IOSXEOperationalAction CRs exactly once.
//
// Each kind is destructive or near-destructive, so we do NOT retry on
// transient failure. Operators submit a new CR to re-attempt.
type Reconciler struct {
	Client          client.Client
	Reader          client.Reader
	Recorder        record.EventRecorder
	Scheme          *runtime.Scheme
	DeviceName      string
	DeviceNamespace string
	GNOI            gnoi.Provider

	// Now is injected for tests.
	Now func() time.Time
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
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

	// Terminal phases are no-ops — actions execute exactly once.
	switch act.Status.Phase {
	case opsv1alpha1.ActionPhaseSucceeded, opsv1alpha1.ActionPhaseFailed, opsv1alpha1.ActionPhaseRejected:
		if err := r.removeFinalizer(ctx, &act); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	if !act.DeletionTimestamp.IsZero() {
		if act.Status.Phase == opsv1alpha1.ActionPhaseRunning || act.Status.InvocationID != "" {
			logger.WithField("invocationID", act.Status.InvocationID).
				Warn("IOSXEOperationalAction deletion requested after invocation; preserving CR to avoid losing destructive-operation audit trail")
			r.recordEvent(&act, corev1.EventTypeWarning, "DeletionPending",
				"delete requested after device-side action was invoked; CR is retained for audit")
			return reconcile.Result{}, nil
		}
		if err := r.removeFinalizer(ctx, &act); err != nil {
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}
	if err := r.ensureFinalizer(ctx, &act); err != nil {
		return reconcile.Result{}, err
	}
	if act.Status.Phase == opsv1alpha1.ActionPhaseRunning {
		logger.WithField("invocationID", act.Status.InvocationID).
			Info("IOSXEOperationalAction already running; refusing duplicate dispatch")
		return reconcile.Result{}, nil
	}

	now := r.now()

	// Confirmation guard: prevent typo-driven actions against the wrong device.
	if act.Spec.Confirm != act.Spec.DeviceRef.Name {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseRejected, "ConfirmMismatch",
			fmt.Sprintf("spec.confirm=%q does not match spec.deviceRef.name=%q",
				act.Spec.Confirm, act.Spec.DeviceRef.Name), nil, now)
	}
	if err := validateActionRequest(act.Spec.Action); err != nil {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseRejected, "InvalidAction", err.Error(), nil, now)
	}

	if r.GNOI == nil {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, "NoGNOIProvider",
			"gnoi provider not configured on reconciler", nil, now)
	}
	gnoiClient, err := r.GNOI.GNOIClient(ctx)
	if err != nil {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, "GNOIClient", err.Error(), nil, now)
	}

	// Mark Running before dispatch so the status reflects "device touched".
	if err := r.markRunning(ctx, &act, now); err != nil {
		return reconcile.Result{}, err
	}

	logger.Warn("dispatching IOSXEOperationalAction gNOI RPC")
	result, kindErr := r.dispatch(ctx, &act, gnoiClient)
	if kindErr != nil {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, "ActionFailed", kindErr.Error(), result, now)
	}
	return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseSucceeded, "Succeeded",
		"action completed successfully", result, now)
}

func (r *Reconciler) dispatch(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, gc *gnoi.Client) ([]byte, error) {
	kind := act.Spec.Action.Kind
	switch kind {
	case opsv1alpha1.ActionKindReboot:
		return r.runReboot(ctx, act, gc)
	case opsv1alpha1.ActionKindCancelReboot:
		return r.runCancelReboot(ctx, act, gc)
	case opsv1alpha1.ActionKindKillProcess:
		return r.runKillProcess(ctx, act, gc)
	case opsv1alpha1.ActionKindFilePut:
		return r.runFilePut(ctx, act, gc)
	case opsv1alpha1.ActionKindFileRemove:
		return r.runFileRemove(ctx, act, gc)
	case opsv1alpha1.ActionKindFactoryReset:
		return r.runFactoryReset(ctx, act, gc)
	default:
		return nil, fmt.Errorf("unsupported action kind %q", kind)
	}
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

func (r *Reconciler) runFilePut(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, gc *gnoi.Client) ([]byte, error) {
	args := act.Spec.Action.FilePut
	if args == nil {
		return nil, errors.New("action.filePut is required")
	}
	if args.ConfigMapName == "" {
		return nil, errors.New("action.filePut.configMapName is required")
	}
	var cm corev1.ConfigMap
	if err := r.Client.Get(ctx, types.NamespacedName{Namespace: act.Namespace, Name: args.ConfigMapName}, &cm); err != nil {
		return nil, fmt.Errorf("filePut: get ConfigMap %s/%s: %w", act.Namespace, args.ConfigMapName, err)
	}
	data, ok := cm.BinaryData["content"]
	if !ok {
		return nil, fmt.Errorf("filePut: ConfigMap %s/%s has no binaryData[\"content\"]", act.Namespace, args.ConfigMapName)
	}
	return nil, gc.Put(ctx, args.Path, bytes.NewReader(data), gnoi.PutOpts{Permissions: args.Permissions})
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

func (r *Reconciler) markRunning(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, now time.Time) error {
	_, err := r.updateStatus(ctx, act, func(cur *opsv1alpha1.IOSXEOperationalAction) {
		cur.Status.Phase = opsv1alpha1.ActionPhaseRunning
		if cur.Status.StartTime == nil {
			cur.Status.StartTime = &metav1.Time{Time: now}
		}
		if cur.Status.InvocationID == "" {
			cur.Status.InvocationID = invocationID(cur)
		}
		cur.Status.Message = "action running"
		r.setReady(cur, metav1.ConditionFalse, "Running", "action running", now)
	}, reconcile.Result{})
	if err == nil {
		log.G(ctx).WithField("operationalAction", client.ObjectKeyFromObject(act).String()).
			WithField("kind", act.Spec.Action.Kind).
			Info("IOSXEOperationalAction phase advanced to Running")
		recordActionTransition(act.Spec.DeviceRef.Name, string(act.Spec.Action.Kind), string(opsv1alpha1.ActionPhaseRunning), "Running")
		r.recordEvent(act, corev1.EventTypeNormal, "Running", r.actionSummary(act))
	}
	return err
}

func (r *Reconciler) terminal(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, phase opsv1alpha1.ActionPhase, reason, message string, result []byte, now time.Time) (reconcile.Result, error) {
	res, err := r.updateStatus(ctx, act, func(cur *opsv1alpha1.IOSXEOperationalAction) {
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

func (r *Reconciler) updateStatus(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, mutate func(*opsv1alpha1.IOSXEOperationalAction), result reconcile.Result) (reconcile.Result, error) {
	err := retry.RetryOnConflict(retry.DefaultBackoff, func() error {
		var cur opsv1alpha1.IOSXEOperationalAction
		reader := r.Reader
		if reader == nil {
			reader = r.Client
		}
		if err := reader.Get(ctx, client.ObjectKeyFromObject(act), &cur); err != nil {
			return err
		}
		mutate(&cur)
		cur.Status.ObservedGeneration = cur.Generation
		return r.Client.Status().Update(ctx, &cur)
	})
	if err != nil {
		return result, fmt.Errorf("update action status: %w", err)
	}
	return result, nil
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
	if argsBlocks != 1 {
		return fmt.Errorf("action.kind %q requires exactly one matching args block; got %d", action.Kind, argsBlocks)
	}
	switch action.Kind {
	case opsv1alpha1.ActionKindReboot:
		if action.Reboot == nil {
			return errors.New("action.kind Reboot requires action.reboot")
		}
	case opsv1alpha1.ActionKindCancelReboot:
		if action.CancelReboot == nil {
			return errors.New("action.kind CancelReboot requires action.cancelReboot")
		}
	case opsv1alpha1.ActionKindKillProcess:
		if action.KillProcess == nil {
			return errors.New("action.kind KillProcess requires action.killProcess")
		}
	case opsv1alpha1.ActionKindFilePut:
		if action.FilePut == nil {
			return errors.New("action.kind FilePut requires action.filePut")
		}
	case opsv1alpha1.ActionKindFileRemove:
		if action.FileRemove == nil {
			return errors.New("action.kind FileRemove requires action.fileRemove")
		}
	case opsv1alpha1.ActionKindFactoryReset:
		if action.FactoryReset == nil {
			return errors.New("action.kind FactoryReset requires action.factoryReset")
		}
	default:
		return fmt.Errorf("unsupported action kind %q", action.Kind)
	}
	return nil
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

// jsonResult is a helper for kinds that want to surface device-side
// data on Success (currently unused — Reboot/FilePut/etc. don't return
// a structured payload). Kept for future kinds that do.
func jsonResult(payload any) []byte {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil
	}
	return b
}

var _ = jsonResult // referenced by future kinds
