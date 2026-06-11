// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package provider

import (
	"context"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/sonic"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

const (
	defaultSONICConfigReconcileInterval = 5 * time.Second
	defaultSONICDriftDetectInterval     = 5 * time.Minute
	minSONICDriftDetectInterval         = 30 * time.Second
)

type SONICConfigApplier interface {
	Health(context.Context) error
	Apply(context.Context, sonic.OpenConfigIntent) (sonic.ApplyResult, error)
	Close() error
}

func NewSONICConfigApplier(spec *ciskov1.DeviceSpec, password string) (SONICConfigApplier, error) {
	return sonic.NewOpenConfigApplier(spec, password)
}

// SONICConfigReconciler resolves SONICConfig CRs for one CiscoDevice and
// applies OpenConfig operations through SONiC gNMI.
type SONICConfigReconciler struct {
	Client     client.Client
	DeviceName string
	Applier    SONICConfigApplier
	Recorder   record.EventRecorder
	Interval   time.Duration
}

func (r *SONICConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1alpha1.SONICConfig{}).
		Complete(r)
}

func (r *SONICConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr configv1alpha1.SONICConfig
	if err := r.Client.Get(ctx, req.NamespacedName, &cr); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if cr.Spec.DeviceRef.Name != r.DeviceName {
		return ctrl.Result{}, nil
	}
	if err := r.reconcileOne(ctx, &cr); err != nil {
		log.G(ctx).WithError(err).WithField("sonicconfig", req.String()).Warn("reconcile SONICConfig failed")
	}
	return ctrl.Result{RequeueAfter: driftDetectIntervalSONIC(&cr)}, nil
}

func (r *SONICConfigReconciler) Run(ctx context.Context) error {
	if r.Client == nil {
		return fmt.Errorf("SONICConfigReconciler: nil Client")
	}
	if r.DeviceName == "" {
		return fmt.Errorf("SONICConfigReconciler: empty DeviceName")
	}
	interval := r.Interval
	if interval <= 0 {
		interval = defaultSONICConfigReconcileInterval
	}
	logger := log.G(ctx).WithField("component", "sonic-config-reconciler").WithField("device", r.DeviceName)
	logger.WithField("interval", interval).Info("starting SONICConfig reconcile loop")
	r.reconcileAll(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping SONICConfig reconcile loop")
			return ctx.Err()
		case <-ticker.C:
			r.reconcileAll(ctx)
		}
	}
}

func (r *SONICConfigReconciler) reconcileAll(ctx context.Context) {
	var list configv1alpha1.SONICConfigList
	if err := r.Client.List(ctx, &list); err != nil {
		log.G(ctx).WithError(err).Warn("list SONICConfig failed; skipping tick")
		return
	}
	items := make([]*configv1alpha1.SONICConfig, 0, len(list.Items))
	for i := range list.Items {
		if list.Items[i].Spec.DeviceRef.Name == r.DeviceName {
			items = append(items, &list.Items[i])
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].Namespace != items[j].Namespace {
			return items[i].Namespace < items[j].Namespace
		}
		return items[i].Name < items[j].Name
	})
	for _, cr := range items {
		if err := r.reconcileOne(ctx, cr); err != nil {
			log.G(ctx).WithError(err).WithField("name", cr.Name).WithField("namespace", cr.Namespace).Warn("reconcile SONICConfig failed")
		}
	}
}

func (r *SONICConfigReconciler) reconcileOne(ctx context.Context, cr *configv1alpha1.SONICConfig) error {
	if cr.Spec.DriftPolicy == configv1alpha1.DriftPolicyPause {
		return r.recordSONICPhase(ctx, cr, "Paused", "Paused", "driftPolicy is pause", nil, "")
	}
	if err := validateSONICModelSource(cr); err != nil {
		return r.recordSONICFailure(ctx, cr, fmt.Sprintf("modelSource: %v", err))
	}
	ops, err := sonic.LoadSource(ctx, r.Client, cr.Namespace, cr.Spec.Source)
	if err != nil {
		return r.recordSONICFailure(ctx, cr, fmt.Sprintf("source: %v", err))
	}
	if err := sonic.ValidateManagedPaths(cr.Spec.ManagedPaths); err != nil {
		return r.recordSONICFailure(ctx, cr, err.Error())
	}
	if err := sonic.ValidateOperations(ops, cr.Spec.ManagedPaths); err != nil {
		return r.recordSONICFailure(ctx, cr, err.Error())
	}
	h, err := sonic.CanonicalHash(map[string]any{
		"device":       cr.Spec.DeviceRef.Name,
		"managedPaths": cr.Spec.ManagedPaths,
		"operations":   ops,
	})
	if err != nil {
		return r.recordSONICFailure(ctx, cr, fmt.Sprintf("hash: %v", err))
	}
	if cr.Status.ObservedGeneration == cr.Generation && cr.Status.LastAppliedHash == h && cr.Status.Phase == "InSync" && !dueForSONICDriftCheck(cr) {
		return nil
	}
	if r.Applier == nil {
		return r.recordSONICPhase(ctx, cr, "Pending", "NoApplier", "SONiC config applier is not configured", nil, h)
	}
	intent := sonic.OpenConfigIntent{
		DeviceName:   cr.Spec.DeviceRef.Name,
		ManagedPaths: append([]string(nil), cr.Spec.ManagedPaths...),
		Operations:   ops,
		DriftPolicy:  cr.Spec.DriftPolicy,
	}
	if intent.DriftPolicy == "" {
		intent.DriftPolicy = configv1alpha1.DriftPolicyRevert
	}
	result, err := r.Applier.Apply(ctx, intent)
	if err != nil {
		_ = r.recordSONICResult(ctx, cr, result, h, "Failed", err.Error())
		return err
	}
	phase := "InSync"
	msg := "SONiC reconciled to declared OpenConfig intent"
	if len(result.Drift) > 0 || intent.DriftPolicy == configv1alpha1.DriftPolicyReport {
		phase = "Drifted"
		msg = "SONiC drift reported without writes"
	}
	return r.recordSONICResult(ctx, cr, result, h, phase, msg)
}

func validateSONICModelSource(cr *configv1alpha1.SONICConfig) error {
	if cr == nil || cr.Spec.ModelSource == nil {
		return nil
	}
	if cr.Spec.ModelSource.Format != configv1alpha1.NetAsCodeModelFormatOpenConfig {
		return fmt.Errorf("format must be %q for SONICConfig, got %q", configv1alpha1.NetAsCodeModelFormatOpenConfig, cr.Spec.ModelSource.Format)
	}
	return nil
}

func (r *SONICConfigReconciler) recordSONICFailure(ctx context.Context, cr *configv1alpha1.SONICConfig, msg string) error {
	return r.recordSONICPhase(ctx, cr, "Failed", "ReconcileFailed", msg, nil, "")
}

func (r *SONICConfigReconciler) recordSONICPhase(ctx context.Context, cr *configv1alpha1.SONICConfig, phase, reason, msg string, result *sonic.ApplyResult, hash string) error {
	if result == nil {
		result = &sonic.ApplyResult{}
	}
	return r.recordSONICResult(ctx, cr, *result, hash, phase, msgWithSONICReason(reason, msg))
}

func (r *SONICConfigReconciler) recordSONICResult(ctx context.Context, cr *configv1alpha1.SONICConfig, result sonic.ApplyResult, hash, phase, msg string) error {
	updated := cr.DeepCopy()
	updated.Status.Phase = phase
	updated.Status.ObservedGeneration = cr.Generation
	now := metav1.Now()
	updated.Status.LastDeviceCheck = &now
	if phase == "InSync" || phase == "Drifted" {
		updated.Status.LastAppliedHash = hash
		updated.Status.LastAppliedTime = &now
	}
	updated.Status.FamilyStatus = updated.Status.FamilyStatus[:0]
	for _, fs := range result.FamilyResults {
		updated.Status.FamilyStatus = append(updated.Status.FamilyStatus, configv1alpha1.FamilyStatus{Name: fs.Name, State: fs.State, Entries: fs.Entries, OpCount: fs.OpCount, Message: fs.Message})
	}
	updated.Status.Drift = updated.Status.Drift[:0]
	for i, d := range result.Drift {
		if i >= 50 {
			break
		}
		updated.Status.Drift = append(updated.Status.Drift, configv1alpha1.DriftEntry{Family: d.Family, Path: d.Path, Desired: d.Desired, Observed: d.Observed, Detected: now})
	}
	ready := metav1.ConditionTrue
	reason := "Succeeded"
	if phase != "InSync" {
		ready = metav1.ConditionFalse
		reason = phase
	}
	setSONICCondition(&updated.Status, metav1.Condition{Type: "Ready", Status: ready, Reason: reason, Message: msg, ObservedGeneration: cr.Generation})
	if r.Recorder != nil && phase != cr.Status.Phase {
		eventType := corev1.EventTypeNormal
		if ready == metav1.ConditionFalse {
			eventType = corev1.EventTypeWarning
		}
		r.Recorder.Eventf(cr, eventType, reason, msg)
	}
	return ignoreConflict(r.Client.Status().Update(ctx, updated))
}

func msgWithSONICReason(reason, msg string) string {
	if msg == "" {
		return reason
	}
	return msg
}

func setSONICCondition(status *configv1alpha1.SONICConfigStatus, c metav1.Condition) {
	now := metav1.Now()
	for i := range status.Conditions {
		if status.Conditions[i].Type == c.Type {
			if status.Conditions[i].Status != c.Status || status.Conditions[i].Reason != c.Reason || status.Conditions[i].Message != c.Message {
				c.LastTransitionTime = now
			} else {
				c.LastTransitionTime = status.Conditions[i].LastTransitionTime
			}
			status.Conditions[i] = c
			return
		}
	}
	c.LastTransitionTime = now
	status.Conditions = append(status.Conditions, c)
}

func driftDetectIntervalSONIC(cr *configv1alpha1.SONICConfig) time.Duration {
	if cr.Spec.DriftDetectInterval == "" {
		return defaultSONICDriftDetectInterval
	}
	d, err := time.ParseDuration(cr.Spec.DriftDetectInterval)
	if err != nil {
		return defaultSONICDriftDetectInterval
	}
	if d < minSONICDriftDetectInterval {
		return minSONICDriftDetectInterval
	}
	return d
}

func dueForSONICDriftCheck(cr *configv1alpha1.SONICConfig) bool {
	if cr.Status.LastDeviceCheck == nil {
		return true
	}
	return time.Since(cr.Status.LastDeviceCheck.Time) >= driftDetectIntervalSONIC(cr)
}
