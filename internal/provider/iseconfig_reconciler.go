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
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/ise"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

const (
	defaultISEConfigReconcileInterval = 5 * time.Second
	defaultISEDriftDetectInterval     = 5 * time.Minute
	minISEDriftDetectInterval         = 30 * time.Second
)

type ISEConfigApplier interface {
	Health(context.Context) error
	Apply(context.Context, ise.Intent) (ise.ApplyResult, error)
	Close() error
}

func NewISEConfigApplier(spec *ciskov1.DeviceSpec, password string) (ISEConfigApplier, error) {
	return ise.NewNetAsCodeApplier(spec, password)
}

// ISEConfigReconciler resolves ISEConfig CRs for one CiscoDevice and applies
// the Network as Code ISE model through the ISE ERS/API channel.
type ISEConfigReconciler struct {
	Client     client.Client
	DeviceName string
	Applier    ISEConfigApplier
	Recorder   record.EventRecorder
	Interval   time.Duration
}

func (r *ISEConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1alpha1.ISEConfig{}).
		Complete(r)
}

func (r *ISEConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr configv1alpha1.ISEConfig
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
		log.G(ctx).WithError(err).WithField("iseconfig", req.String()).Warn("reconcile ISEConfig failed")
	}
	return ctrl.Result{RequeueAfter: driftDetectIntervalISE(&cr)}, nil
}

func (r *ISEConfigReconciler) Run(ctx context.Context) error {
	if r.Client == nil {
		return fmt.Errorf("ISEConfigReconciler: nil Client")
	}
	if r.DeviceName == "" {
		return fmt.Errorf("ISEConfigReconciler: empty DeviceName")
	}
	interval := r.Interval
	if interval <= 0 {
		interval = defaultISEConfigReconcileInterval
	}
	logger := log.G(ctx).WithField("component", "ise-config-reconciler").WithField("device", r.DeviceName)
	logger.WithField("interval", interval).Info("starting ISEConfig reconcile loop")
	r.reconcileAll(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping ISEConfig reconcile loop")
			return ctx.Err()
		case <-ticker.C:
			r.reconcileAll(ctx)
		}
	}
}

func (r *ISEConfigReconciler) reconcileAll(ctx context.Context) {
	var list configv1alpha1.ISEConfigList
	if err := r.Client.List(ctx, &list); err != nil {
		log.G(ctx).WithError(err).Warn("list ISEConfig failed; skipping tick")
		return
	}
	items := make([]*configv1alpha1.ISEConfig, 0, len(list.Items))
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
			log.G(ctx).WithError(err).WithField("name", cr.Name).WithField("namespace", cr.Namespace).Warn("reconcile ISEConfig failed")
		}
	}
}

func (r *ISEConfigReconciler) reconcileOne(ctx context.Context, cr *configv1alpha1.ISEConfig) error {
	if cr.Spec.DriftPolicy == configv1alpha1.DriftPolicyPause {
		return r.recordISEPhase(ctx, cr, "Paused", "Paused", "driftPolicy is pause", nil, "")
	}
	if err := validateISEModelSource(cr); err != nil {
		return r.recordISEFailure(ctx, cr, fmt.Sprintf("modelSource: %v", err))
	}
	configuration, err := ise.LoadSource(ctx, r.Client, cr.Namespace, cr.Spec.DeviceRef.Name, cr.Spec.Source)
	if err != nil {
		return r.recordISEFailure(ctx, cr, fmt.Sprintf("source: %v", err))
	}
	if err := r.mergeISESecretRefs(ctx, cr, configuration); err != nil {
		return r.recordISEFailure(ctx, cr, fmt.Sprintf("secretRefs: %v", err))
	}
	if err := ise.ValidateManagedFamilies(cr.Spec.ManagedFamilies); err != nil {
		return r.recordISEFailure(ctx, cr, err.Error())
	}
	if err := ise.ValidateModel(configuration, cr.Spec.ManagedFamilies); err != nil {
		return r.recordISEFailure(ctx, cr, err.Error())
	}
	h, err := ise.CanonicalHash(map[string]any{
		"device":          cr.Spec.DeviceRef.Name,
		"managedFamilies": cr.Spec.ManagedFamilies,
		"configuration":   configuration,
	})
	if err != nil {
		return r.recordISEFailure(ctx, cr, fmt.Sprintf("hash: %v", err))
	}
	if cr.Status.ObservedGeneration == cr.Generation && cr.Status.LastAppliedHash == h && cr.Status.Phase == "InSync" && !dueForISEDriftCheck(cr) {
		return nil
	}
	if r.Applier == nil {
		return r.recordISEPhase(ctx, cr, "Pending", "NoApplier", "ISE config applier is not configured", nil, h)
	}
	intent := ise.Intent{
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
		_ = r.recordISEResult(ctx, cr, result, h, "Failed", err.Error())
		return err
	}
	phase := "InSync"
	msg := "ISE reconciled to declared Network as Code intent"
	if len(result.Drift) > 0 || intent.DriftPolicy == configv1alpha1.DriftPolicyReport {
		phase = "Drifted"
		msg = "ISE drift reported without writes"
	}
	return r.recordISEResult(ctx, cr, result, h, phase, msg)
}

func validateISEModelSource(cr *configv1alpha1.ISEConfig) error {
	if cr == nil || cr.Spec.ModelSource == nil {
		return nil
	}
	if cr.Spec.ModelSource.Format != configv1alpha1.NetAsCodeModelFormatISE {
		return fmt.Errorf("format must be %q for ISEConfig, got %q", configv1alpha1.NetAsCodeModelFormatISE, cr.Spec.ModelSource.Format)
	}
	return nil
}

func (r *ISEConfigReconciler) mergeISESecretRefs(ctx context.Context, cr *configv1alpha1.ISEConfig, configuration map[string]any) error {
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
		configuration[ref.Family] = deepMergeMaps(base, m)
	}
	return nil
}

func deepMergeMaps(left, right map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range left {
		out[k] = v
	}
	for k, rv := range right {
		if lm, ok := out[k].(map[string]any); ok {
			if rm, ok := rv.(map[string]any); ok {
				out[k] = deepMergeMaps(lm, rm)
				continue
			}
		}
		out[k] = rv
	}
	return out
}

func (r *ISEConfigReconciler) recordISEFailure(ctx context.Context, cr *configv1alpha1.ISEConfig, msg string) error {
	return r.recordISEPhase(ctx, cr, "Failed", "ReconcileFailed", msg, nil, "")
}

func (r *ISEConfigReconciler) recordISEPhase(ctx context.Context, cr *configv1alpha1.ISEConfig, phase, reason, msg string, result *ise.ApplyResult, hash string) error {
	if result == nil {
		result = &ise.ApplyResult{}
	}
	return r.recordISEResult(ctx, cr, *result, hash, phase, msgWithReason(reason, msg))
}

func (r *ISEConfigReconciler) recordISEResult(ctx context.Context, cr *configv1alpha1.ISEConfig, result ise.ApplyResult, hash, phase, msg string) error {
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
	setISECondition(&updated.Status, metav1.Condition{Type: "Ready", Status: ready, Reason: reason, Message: msg, ObservedGeneration: cr.Generation})
	if r.Recorder != nil && phase != cr.Status.Phase {
		eventType := corev1.EventTypeNormal
		if ready == metav1.ConditionFalse {
			eventType = corev1.EventTypeWarning
		}
		r.Recorder.Eventf(cr, eventType, reason, msg)
	}
	return ignoreConflict(r.Client.Status().Update(ctx, updated))
}

func msgWithReason(reason, msg string) string {
	if msg == "" {
		return reason
	}
	return msg
}

func setISECondition(status *configv1alpha1.ISEConfigStatus, c metav1.Condition) {
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

func driftDetectIntervalISE(cr *configv1alpha1.ISEConfig) time.Duration {
	if cr.Spec.DriftDetectInterval == "" {
		return defaultISEDriftDetectInterval
	}
	d, err := time.ParseDuration(cr.Spec.DriftDetectInterval)
	if err != nil {
		return defaultISEDriftDetectInterval
	}
	if d < minISEDriftDetectInterval {
		return minISEDriftDetectInterval
	}
	return d
}

func dueForISEDriftCheck(cr *configv1alpha1.ISEConfig) bool {
	if cr.Status.LastDeviceCheck == nil {
		return true
	}
	return time.Since(cr.Status.LastDeviceCheck.Time) >= driftDetectIntervalISE(cr)
}
