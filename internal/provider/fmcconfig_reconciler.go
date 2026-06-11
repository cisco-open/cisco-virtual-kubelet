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
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/fmc"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

const (
	defaultFMCConfigReconcileInterval = 5 * time.Second
	defaultFMCDriftDetectInterval     = 5 * time.Minute
	minFMCDriftDetectInterval         = 30 * time.Second
)

type FMCConfigApplier interface {
	Health(context.Context) error
	Apply(context.Context, fmc.Intent) (fmc.ApplyResult, error)
	Close() error
}

func NewFMCConfigApplier(spec *ciskov1.DeviceSpec, password string) (FMCConfigApplier, error) {
	return fmc.NewNetAsCodeApplier(spec, password)
}

// FMCConfigReconciler resolves FMCConfig CRs for one CiscoDevice and applies
// the Network as Code FMC model through the FMC ERS/API channel.
type FMCConfigReconciler struct {
	Client     client.Client
	DeviceName string
	Applier    FMCConfigApplier
	Recorder   record.EventRecorder
	Interval   time.Duration
}

func (r *FMCConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1alpha1.FMCConfig{}).
		Complete(r)
}

func (r *FMCConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr configv1alpha1.FMCConfig
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
		log.G(ctx).WithError(err).WithField("fmcconfig", req.String()).Warn("reconcile FMCConfig failed")
	}
	return ctrl.Result{RequeueAfter: driftDetectIntervalFMC(&cr)}, nil
}

func (r *FMCConfigReconciler) Run(ctx context.Context) error {
	if r.Client == nil {
		return fmt.Errorf("FMCConfigReconciler: nil Client")
	}
	if r.DeviceName == "" {
		return fmt.Errorf("FMCConfigReconciler: empty DeviceName")
	}
	interval := r.Interval
	if interval <= 0 {
		interval = defaultFMCConfigReconcileInterval
	}
	logger := log.G(ctx).WithField("component", "fmc-config-reconciler").WithField("device", r.DeviceName)
	logger.WithField("interval", interval).Info("starting FMCConfig reconcile loop")
	r.reconcileAll(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping FMCConfig reconcile loop")
			return ctx.Err()
		case <-ticker.C:
			r.reconcileAll(ctx)
		}
	}
}

func (r *FMCConfigReconciler) reconcileAll(ctx context.Context) {
	var list configv1alpha1.FMCConfigList
	if err := r.Client.List(ctx, &list); err != nil {
		log.G(ctx).WithError(err).Warn("list FMCConfig failed; skipping tick")
		return
	}
	items := make([]*configv1alpha1.FMCConfig, 0, len(list.Items))
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
			log.G(ctx).WithError(err).WithField("name", cr.Name).WithField("namespace", cr.Namespace).Warn("reconcile FMCConfig failed")
		}
	}
}

func (r *FMCConfigReconciler) reconcileOne(ctx context.Context, cr *configv1alpha1.FMCConfig) error {
	if cr.Spec.DriftPolicy == configv1alpha1.DriftPolicyPause {
		return r.recordFMCPhase(ctx, cr, "Paused", "Paused", "driftPolicy is pause", nil, "")
	}
	if err := validateFMCModelSource(cr); err != nil {
		return r.recordFMCFailure(ctx, cr, fmt.Sprintf("modelSource: %v", err))
	}
	configuration, err := fmc.LoadSource(ctx, r.Client, cr.Namespace, cr.Spec.DeviceRef.Name, cr.Spec.Source)
	if err != nil {
		return r.recordFMCFailure(ctx, cr, fmt.Sprintf("source: %v", err))
	}
	if err := r.mergeFMCSecretRefs(ctx, cr, configuration); err != nil {
		return r.recordFMCFailure(ctx, cr, fmt.Sprintf("secretRefs: %v", err))
	}
	if err := fmc.ValidateManagedFamilies(cr.Spec.ManagedFamilies); err != nil {
		return r.recordFMCFailure(ctx, cr, err.Error())
	}
	if err := fmc.ValidateModel(configuration, cr.Spec.ManagedFamilies); err != nil {
		return r.recordFMCFailure(ctx, cr, err.Error())
	}
	h, err := fmc.CanonicalHash(map[string]any{
		"device":          cr.Spec.DeviceRef.Name,
		"managedFamilies": cr.Spec.ManagedFamilies,
		"configuration":   configuration,
	})
	if err != nil {
		return r.recordFMCFailure(ctx, cr, fmt.Sprintf("hash: %v", err))
	}
	if cr.Status.ObservedGeneration == cr.Generation && cr.Status.LastAppliedHash == h && cr.Status.Phase == "InSync" && !dueForFMCDriftCheck(cr) {
		return nil
	}
	if r.Applier == nil {
		return r.recordFMCPhase(ctx, cr, "Pending", "NoApplier", "FMC config applier is not configured", nil, h)
	}
	intent := fmc.Intent{
		DeviceName:      cr.Spec.DeviceRef.Name,
		ManagedFamilies: append([]string(nil), cr.Spec.ManagedFamilies...),
		Configuration:   configuration,
		DriftPolicy:     cr.Spec.DriftPolicy,
	}
	if intent.DriftPolicy == "" {
		intent.DriftPolicy = configv1alpha1.DriftPolicyRevert
	}
	result, err := r.Applier.Apply(ctx, intent)
	if err != nil {
		_ = r.recordFMCResult(ctx, cr, result, h, "Failed", err.Error())
		return err
	}
	phase := "InSync"
	msg := "FMC reconciled to declared Network as Code intent"
	if len(result.Drift) > 0 || intent.DriftPolicy == configv1alpha1.DriftPolicyReport {
		phase = "Drifted"
		msg = "FMC drift reported without writes"
	}
	return r.recordFMCResult(ctx, cr, result, h, phase, msg)
}

func validateFMCModelSource(cr *configv1alpha1.FMCConfig) error {
	if cr == nil || cr.Spec.ModelSource == nil {
		return nil
	}
	if cr.Spec.ModelSource.Format != configv1alpha1.NetAsCodeModelFormatFMC {
		return fmt.Errorf("format must be %q for FMCConfig, got %q", configv1alpha1.NetAsCodeModelFormatFMC, cr.Spec.ModelSource.Format)
	}
	return nil
}

func (r *FMCConfigReconciler) mergeFMCSecretRefs(ctx context.Context, cr *configv1alpha1.FMCConfig, configuration map[string]any) error {
	for _, ref := range cr.Spec.SecretRefs {
		if ref.Family == "" || ref.Name == "" || ref.Key == "" {
			return fmt.Errorf("family, name, and key are required")
		}
		managed := false
		for _, fam := range cr.Spec.ManagedFamilies {
			if fam == ref.Family {
				managed = true
				break
			}
		}
		if !managed {
			return fmt.Errorf("secretRef family %q is not listed in managedFamilies", ref.Family)
		}
		var sec corev1.Secret
		if err := r.Client.Get(ctx, types.NamespacedName{Namespace: cr.Namespace, Name: ref.Name}, &sec); err != nil {
			return fmt.Errorf("get Secret %s/%s: %w", cr.Namespace, ref.Name, err)
		}
		raw, ok := sec.Data[ref.Key]
		if !ok {
			return fmt.Errorf("Secret %s/%s does not contain key %q", cr.Namespace, ref.Name, ref.Key)
		}
		var snippet any
		if err := yaml.Unmarshal(raw, &snippet); err != nil {
			return fmt.Errorf("parse Secret %s/%s key %q: %w", cr.Namespace, ref.Name, ref.Key, err)
		}
		m, ok := snippet.(map[string]any)
		if !ok {
			return fmt.Errorf("Secret %s/%s key %q must decode to a mapping", cr.Namespace, ref.Name, ref.Key)
		}
		base, _ := configuration[ref.Family].(map[string]any)
		if base == nil {
			base = map[string]any{}
		}
		configuration[ref.Family] = deepMergeFMCMaps(base, m)
	}
	return nil
}

func deepMergeFMCMaps(left, right map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range left {
		out[k] = v
	}
	for k, rv := range right {
		if lm, ok := out[k].(map[string]any); ok {
			if rm, ok := rv.(map[string]any); ok {
				out[k] = deepMergeFMCMaps(lm, rm)
				continue
			}
		}
		out[k] = rv
	}
	return out
}

func (r *FMCConfigReconciler) recordFMCFailure(ctx context.Context, cr *configv1alpha1.FMCConfig, msg string) error {
	return r.recordFMCPhase(ctx, cr, "Failed", "ReconcileFailed", msg, nil, "")
}

func (r *FMCConfigReconciler) recordFMCPhase(ctx context.Context, cr *configv1alpha1.FMCConfig, phase, reason, msg string, result *fmc.ApplyResult, hash string) error {
	if result == nil {
		result = &fmc.ApplyResult{}
	}
	return r.recordFMCResult(ctx, cr, *result, hash, phase, msgWithFMCReason(reason, msg))
}

func (r *FMCConfigReconciler) recordFMCResult(ctx context.Context, cr *configv1alpha1.FMCConfig, result fmc.ApplyResult, hash, phase, msg string) error {
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
	setFMCCondition(&updated.Status, metav1.Condition{Type: "Ready", Status: ready, Reason: reason, Message: msg, ObservedGeneration: cr.Generation})
	if r.Recorder != nil && phase != cr.Status.Phase {
		eventType := corev1.EventTypeNormal
		if ready == metav1.ConditionFalse {
			eventType = corev1.EventTypeWarning
		}
		r.Recorder.Eventf(cr, eventType, reason, msg)
	}
	return ignoreConflict(r.Client.Status().Update(ctx, updated))
}

func msgWithFMCReason(reason, msg string) string {
	if msg == "" {
		return reason
	}
	return msg
}

func setFMCCondition(status *configv1alpha1.FMCConfigStatus, c metav1.Condition) {
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

func driftDetectIntervalFMC(cr *configv1alpha1.FMCConfig) time.Duration {
	if cr.Spec.DriftDetectInterval == "" {
		return defaultFMCDriftDetectInterval
	}
	d, err := time.ParseDuration(cr.Spec.DriftDetectInterval)
	if err != nil {
		return defaultFMCDriftDetectInterval
	}
	if d < minFMCDriftDetectInterval {
		return minFMCDriftDetectInterval
	}
	return d
}

func dueForFMCDriftCheck(cr *configv1alpha1.FMCConfig) bool {
	if cr.Status.LastDeviceCheck == nil {
		return true
	}
	return time.Since(cr.Status.LastDeviceCheck.Time) >= driftDetectIntervalFMC(cr)
}
