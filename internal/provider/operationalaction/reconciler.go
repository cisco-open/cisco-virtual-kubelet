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

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	opsv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/gnoi"
)

const conditionTypeReady = "Ready"

// GNOIProvider exposes the per-device gNOI client.
type GNOIProvider interface {
	GNOIClient(ctx context.Context) (*gnoi.Client, error)
}

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
	GNOI            GNOIProvider

	// Now is injected for tests.
	Now func() time.Time
}

func (r *Reconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
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
	// Terminal phases are no-ops — actions execute exactly once.
	switch act.Status.Phase {
	case opsv1alpha1.ActionPhaseSucceeded, opsv1alpha1.ActionPhaseFailed, opsv1alpha1.ActionPhaseRejected:
		return reconcile.Result{}, nil
	}

	now := r.now()

	// Confirmation guard: prevent typo-driven actions against the wrong device.
	if act.Spec.Confirm != act.Spec.DeviceRef.Name {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseRejected, "ConfirmMismatch",
			fmt.Sprintf("spec.confirm=%q does not match spec.deviceRef.name=%q",
				act.Spec.Confirm, act.Spec.DeviceRef.Name), nil, now)
	}

	if r.GNOI == nil {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, "NoGNOIProvider",
			"gnoi provider not configured on reconciler", nil, now)
	}
	client, err := r.GNOI.GNOIClient(ctx)
	if err != nil {
		return r.terminal(ctx, &act, opsv1alpha1.ActionPhaseFailed, "GNOIClient", err.Error(), nil, now)
	}

	// Mark Running before dispatch so the status reflects "device touched".
	if err := r.markRunning(ctx, &act, now); err != nil {
		return reconcile.Result{}, err
	}

	result, kindErr := r.dispatch(ctx, &act, client)
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
		args = &opsv1alpha1.RebootActionArgs{}
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
		args = &opsv1alpha1.FactoryResetArgs{}
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
		cur.Status.StartTime = &metav1.Time{Time: now}
		cur.Status.Message = "action running"
		r.setReady(cur, metav1.ConditionFalse, "Running", "action running", now)
	}, reconcile.Result{})
	return err
}

func (r *Reconciler) terminal(ctx context.Context, act *opsv1alpha1.IOSXEOperationalAction, phase opsv1alpha1.ActionPhase, reason, message string, result []byte, now time.Time) (reconcile.Result, error) {
	return r.updateStatus(ctx, act, func(cur *opsv1alpha1.IOSXEOperationalAction) {
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
}

func (r *Reconciler) setReady(act *opsv1alpha1.IOSXEOperationalAction, status metav1.ConditionStatus, reason, message string, now time.Time) {
	cond := metav1.Condition{
		Type:               conditionTypeReady,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: metav1.Time{Time: now},
		ObservedGeneration: act.Generation,
	}
	conds := act.Status.Conditions
	for i, c := range conds {
		if c.Type == cond.Type {
			if c.Status == cond.Status && c.Reason == cond.Reason && c.Message == cond.Message {
				return
			}
			conds[i] = cond
			act.Status.Conditions = conds
			return
		}
	}
	act.Status.Conditions = append(conds, cond)
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
