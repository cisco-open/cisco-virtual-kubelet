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
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/aci"
	"github.com/virtual-kubelet/virtual-kubelet/log"
)

const (
	defaultAPICConfigReconcileInterval = 5 * time.Second
	defaultAPIDriftDetectInterval      = 5 * time.Minute
	minAPIDriftDetectInterval          = 30 * time.Second
)

type APICConfigApplier interface {
	Health(context.Context) error
	Apply(context.Context, aci.Intent) (aci.ApplyResult, error)
	Close() error
}

func NewAPICConfigApplier(spec *ciskov1.DeviceSpec, password string) (APICConfigApplier, error) {
	return aci.NewNetAsCodeApplier(spec, password)
}

// APICConfigReconciler resolves APICConfig CRs for one CiscoDevice and applies
// the Network as Code APIC model through the APIC REST channel.
type APICConfigReconciler struct {
	Client     client.Client
	DeviceName string
	Applier    APICConfigApplier
	Recorder   record.EventRecorder
	Interval   time.Duration
}

func (r *APICConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&configv1alpha1.APICConfig{}).
		Complete(r)
}

func (r *APICConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cr configv1alpha1.APICConfig
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
		log.G(ctx).WithError(err).WithField("apicconfig", req.String()).Warn("reconcile APICConfig failed")
	}
	return ctrl.Result{RequeueAfter: driftDetectIntervalAPI(&cr)}, nil
}

func (r *APICConfigReconciler) Run(ctx context.Context) error {
	if r.Client == nil {
		return fmt.Errorf("APICConfigReconciler: nil Client")
	}
	if r.DeviceName == "" {
		return fmt.Errorf("APICConfigReconciler: empty DeviceName")
	}
	interval := r.Interval
	if interval <= 0 {
		interval = defaultAPICConfigReconcileInterval
	}
	logger := log.G(ctx).WithField("component", "apic-config-reconciler").WithField("device", r.DeviceName)
	logger.WithField("interval", interval).Info("starting APICConfig reconcile loop")
	r.reconcileAll(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logger.Info("stopping APICConfig reconcile loop")
			return ctx.Err()
		case <-ticker.C:
			r.reconcileAll(ctx)
		}
	}
}

func (r *APICConfigReconciler) reconcileAll(ctx context.Context) {
	var list configv1alpha1.APICConfigList
	if err := r.Client.List(ctx, &list); err != nil {
		log.G(ctx).WithError(err).Warn("list APICConfig failed; skipping tick")
		return
	}
	items := make([]*configv1alpha1.APICConfig, 0, len(list.Items))
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
			log.G(ctx).WithError(err).WithField("name", cr.Name).WithField("namespace", cr.Namespace).Warn("reconcile APICConfig failed")
		}
	}
}

func (r *APICConfigReconciler) reconcileOne(ctx context.Context, cr *configv1alpha1.APICConfig) error {
	if cr.Spec.DriftPolicy == configv1alpha1.DriftPolicyPause {
		return r.recordAPIPhase(ctx, cr, "Paused", "Paused", "driftPolicy is pause", nil, "")
	}
	if err := validateAPIModelSource(cr); err != nil {
		return r.recordAPIFailure(ctx, cr, fmt.Sprintf("modelSource: %v", err))
	}
	configuration, err := aci.LoadSource(ctx, r.Client, cr.Namespace, cr.Spec.DeviceRef.Name, cr.Spec.Source)
	if err != nil {
		return r.recordAPIFailure(ctx, cr, fmt.Sprintf("source: %v", err))
	}
	if err := r.mergeAPISecretRefs(ctx, cr, configuration); err != nil {
		return r.recordAPIFailure(ctx, cr, fmt.Sprintf("secretRefs: %v", err))
	}
	if err := aci.ValidateManagedFamilies(cr.Spec.ManagedFamilies); err != nil {
		return r.recordAPIFailure(ctx, cr, err.Error())
	}
	if err := aci.ValidateModel(configuration, cr.Spec.ManagedFamilies); err != nil {
		return r.recordAPIFailure(ctx, cr, err.Error())
	}
	h, err := aci.CanonicalHash(map[string]any{
		"device":          cr.Spec.DeviceRef.Name,
		"managedFamilies": cr.Spec.ManagedFamilies,
		"configuration":   configuration,
	})
	if err != nil {
		return r.recordAPIFailure(ctx, cr, fmt.Sprintf("hash: %v", err))
	}
	if cr.Status.ObservedGeneration == cr.Generation && cr.Status.LastAppliedHash == h && cr.Status.Phase == "InSync" && !dueForAPIDriftCheck(cr) {
		return nil
	}
	if r.Applier == nil {
		return r.recordAPIPhase(ctx, cr, "Pending", "NoApplier", "APIC config applier is not configured", nil, h)
	}
	intent := aci.Intent{
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
		_ = r.recordAPIResult(ctx, cr, result, h, "Failed", err.Error())
		return err
	}
	phase := "InSync"
	msg := "APIC reconciled to declared Network as Code intent"
	if len(result.Drift) > 0 || intent.DriftPolicy == configv1alpha1.DriftPolicyReport {
		phase = "Drifted"
		msg = "APIC drift reported without writes"
	}
	return r.recordAPIResult(ctx, cr, result, h, phase, msg)
}

func validateAPIModelSource(cr *configv1alpha1.APICConfig) error {
	if cr == nil || cr.Spec.ModelSource == nil {
		return nil
	}
	if cr.Spec.ModelSource.Format != configv1alpha1.NetAsCodeModelFormatAPIC {
		return fmt.Errorf("format must be %q for APICConfig, got %q", configv1alpha1.NetAsCodeModelFormatAPIC, cr.Spec.ModelSource.Format)
	}
	return nil
}

func (r *APICConfigReconciler) mergeAPISecretRefs(ctx context.Context, cr *configv1alpha1.APICConfig, configuration map[string]any) error {
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
		configuration[ref.Family] = deepMergeAPIMaps(base, m)
	}
	return nil
}

func deepMergeAPIMaps(left, right map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range left {
		out[k] = v
	}
	for k, rv := range right {
		if lm, ok := out[k].(map[string]any); ok {
			if rm, ok := rv.(map[string]any); ok {
				out[k] = deepMergeAPIMaps(lm, rm)
				continue
			}
		}
		out[k] = rv
	}
	return out
}

func (r *APICConfigReconciler) recordAPIFailure(ctx context.Context, cr *configv1alpha1.APICConfig, msg string) error {
	return r.recordAPIPhase(ctx, cr, "Failed", "ReconcileFailed", msg, nil, "")
}

func (r *APICConfigReconciler) recordAPIPhase(ctx context.Context, cr *configv1alpha1.APICConfig, phase, reason, msg string, result *aci.ApplyResult, hash string) error {
	if result == nil {
		result = &aci.ApplyResult{}
	}
	return r.recordAPIResult(ctx, cr, *result, hash, phase, msgWithAPIReason(reason, msg))
}

func (r *APICConfigReconciler) recordAPIResult(ctx context.Context, cr *configv1alpha1.APICConfig, result aci.ApplyResult, hash, phase, msg string) error {
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
	setAPICondition(&updated.Status, metav1.Condition{Type: "Ready", Status: ready, Reason: reason, Message: msg, ObservedGeneration: cr.Generation})
	if r.Recorder != nil && phase != cr.Status.Phase {
		eventType := corev1.EventTypeNormal
		if ready == metav1.ConditionFalse {
			eventType = corev1.EventTypeWarning
		}
		r.Recorder.Eventf(cr, eventType, reason, msg)
	}
	return ignoreConflict(r.Client.Status().Update(ctx, updated))
}

func msgWithAPIReason(reason, msg string) string {
	if msg == "" {
		return reason
	}
	return msg
}

func setAPICondition(status *configv1alpha1.APICConfigStatus, c metav1.Condition) {
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

func driftDetectIntervalAPI(cr *configv1alpha1.APICConfig) time.Duration {
	if cr.Spec.DriftDetectInterval == "" {
		return defaultAPIDriftDetectInterval
	}
	d, err := time.ParseDuration(cr.Spec.DriftDetectInterval)
	if err != nil {
		return defaultAPIDriftDetectInterval
	}
	if d < minAPIDriftDetectInterval {
		return minAPIDriftDetectInterval
	}
	return d
}

func dueForAPIDriftCheck(cr *configv1alpha1.APICConfig) bool {
	if cr.Status.LastDeviceCheck == nil {
		return true
	}
	return time.Since(cr.Status.LastDeviceCheck.Time) >= driftDetectIntervalAPI(cr)
}
