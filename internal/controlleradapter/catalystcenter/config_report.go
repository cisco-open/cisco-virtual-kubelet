// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"slices"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type configReconciler struct{ a *adapter }

type configSourceSnapshot struct {
	siteSelection                   *siteSourceSelection
	raw                             []byte
	templateFiles                   map[string]string
	floorImages                     map[string][]byte
	templateComparison              *templateInputComparison
	secrets                         *configurationSecretSnapshot
	key                             types.NamespacedName
	uid                             types.UID
	version                         string
	deletion                        *featureDeletionSource
	tagDeletion                     *metadataConfigDeletionSource
	discoveryCleanup                *discoveryCleanupPreparation
	discoveryDrain                  *discoveryConfigDrainContext
	discoveryScope                  *discoverySourceScope
	discoveryScopeControllerVersion string
}

func (r *configReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	a := r.a
	if req.Namespace != a.key.Namespace {
		return ctrl.Result{}, nil
	}
	if handled, err := r.recoverTerminalConfiguration(ctx, req.Name); handled || err != nil {
		if errors.Is(err, errStaleController) {
			return ctrl.Result{}, nil
		}
		if err != nil {
			if !handled {
				err = ctrlclient.IgnoreNotFound(err)
			}
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}
	var config configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, req.NamespacedName, &config); err != nil {
		return ctrl.Result{}, ctrlclient.IgnoreNotFound(err)
	}
	if config.Spec.ControllerRef.Name != a.key.Name {
		return ctrl.Result{}, nil
	}
	if !config.DeletionTimestamp.IsZero() {
		if hasDiscoveryConfigFinalizer(&config) {
			return r.reconcileDiscoveryConfigDeletion(ctx, &config)
		}
		if slices.Contains(config.Finalizers, tagConfigFinalizer) {
			return r.reconcileTagConfigDeletion(ctx, &config)
		}
		if slices.Contains(config.Finalizers, featureConfigFinalizer) {
			return r.reconcileFeatureConfigDeletion(ctx, &config)
		}
		return ctrl.Result{}, nil
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		if errors.Is(err, errStaleController) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	status := *config.Status.DeepCopy()
	status.Phase = configapi.NetworkControllerConfigPhaseFailed
	status.ObservedGeneration = config.Generation
	status.Drift = nil
	status.SectionStatus = nil
	status.LastControllerCheck = nil
	status.ControllerAPIVersion = ""
	source, valid, reason, message := r.evaluate(ctx, nc, &config, &status)
	if len(status.SectionStatus) == 0 {
		state := configapi.NetworkControllerSectionStateUnsupported
		if valid {
			state = configapi.NetworkControllerSectionStateSkipped
		}
		for _, section := range config.Spec.ManagedSections {
			status.SectionStatus = append(status.SectionStatus, configapi.NetworkControllerSectionStatus{Name: section, State: state, Message: message})
		}
	}
	conditions := []metav1.Condition{
		condition(configapi.NetworkControllerConfigConditionAdapterAvailable, true, "AdapterAvailable", "Catalyst Center configuration adapter is available", config.Generation),
		condition(configapi.NetworkControllerConfigConditionIntentValid, valid, reason, message, config.Generation),
		condition(configapi.NetworkControllerConfigConditionReady, status.Phase == configapi.NetworkControllerConfigPhaseInSync, reason, message, config.Generation),
	}
	for _, c := range conditions {
		meta.SetStatusCondition(&status.Conditions, c)
	}
	// Scope publication also binds the controller's idle journal RV. Reuse
	// the ordinary snapshot gate before publishing either discovery scope.
	if source != nil && source.discoveryScopeControllerVersion != "" {
		if err := a.checkConfigurationSnapshot(ctx, &config, source); err != nil {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
	}
	// Never relabel an observation from old intent as current. Read the source
	// and both Kubernetes identities again; the status Update provides config CAS.
	if _, err = a.currentController(ctx); err != nil {
		if errors.Is(err, errStaleController) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	var fresh configapi.NetworkControllerConfig
	if err = a.statusReader.Get(ctx, req.NamespacedName, &fresh); err != nil {
		return ctrl.Result{}, ctrlclient.IgnoreNotFound(err)
	}
	if fresh.UID != config.UID || fresh.Generation != config.Generation || fresh.ResourceVersion != config.ResourceVersion || !fresh.DeletionTimestamp.IsZero() {
		return ctrl.Result{RequeueAfter: time.Second}, nil
	}
	if source != nil && source.key.Name != "" {
		var cm corev1.ConfigMap
		if err = a.statusReader.Get(ctx, source.key, &cm); err != nil {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
		if cm.UID != source.uid || cm.ResourceVersion != source.version {
			return ctrl.Result{RequeueAfter: time.Second}, nil
		}
	}
	fresh.Status = status
	if err = a.statusWriter.Status().Update(ctx, &fresh); err != nil {
		return ctrl.Result{}, err
	}
	interval := 5 * time.Minute
	if config.Spec.DriftDetectInterval != nil {
		interval = config.Spec.DriftDetectInterval.Duration
	}
	if config.Spec.Mode == configapi.NetworkControllerApplyModeApply && status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		interval = 5 * time.Second
	}
	return ctrl.Result{RequeueAfter: interval}, nil
}

// evaluate returns the exact source snapshot rechecked before publication.
// Report mode never mutates the appliance; supported apply mode uses its
// dedicated write-ahead journal and verified resource executor.
func (r *configReconciler) evaluate(ctx context.Context, nc *ciskov1.NetworkController, config *configapi.NetworkControllerConfig, status *configapi.NetworkControllerConfigStatus) (*configSourceSnapshot, bool, string, string) {
	if cleanupFamilyConflict(config) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return nil, true, "CleanupFamilyConflict", "Multiple controller cleanup families are retained; no mutation is authorized"
	}
	// A source-lost acknowledged Create has its own retained metadata contract;
	// it cannot require the projected bytes that have disappeared. This branch
	// never grants native submission and always leaves current readiness false.
	if handled, reason, message := r.evaluateRetainedDiscoveryCredential(ctx, nc, config, status); handled {
		return nil, true, reason, message
	}
	if err := controlleradapter.ValidateConfigContract(nc, config, r.a.intentSecretPath); err != nil {
		return nil, false, "InvalidContract", "Controller intent contract is invalid or incompatible"
	}

	if (discoveryConfigSpecShape(config) || discoveryCredentialDeletionSpecShape(config)) && !hasDiscoveryConfigFinalizer(config) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return nil, true, "CleanupEnrollmentPending", "Manager-owned discovery finalizer must be enrolled before applying Delete-policy intent"
	}
	if featureDeletionShape(config) && config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && !slices.Contains(config.Finalizers, featureConfigFinalizer) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return nil, true, "CleanupEnrollmentPending", "Manager-owned cleanup finalizer must be enrolled before applying Delete-policy intent"
	}
	if tagDeletionShape(config) && config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && !hasTagCleanupFinalizer(config) {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		return nil, true, "CleanupEnrollmentPending", "Manager-owned tag cleanup finalizer must be enrolled before applying Delete-policy intent"
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		if op.Section == "lan_automation" && (strings.HasPrefix(op.Resource, lanLinkAddFamily) || strings.HasPrefix(op.Resource, lanLinkDeleteFamily)) && op.ConfigName == config.Name && op.Phase != "Prepared" {
			if source, handled, reason, message := r.evaluateRetainedLANLink(ctx, nc, config, status); handled {
				return source, true, reason, message
			}
		}
	}
	if supportedLANLinkAddConfig(config) {
		return r.evaluateLANLinkApply(ctx, nc, config, status)
	}
	if source, handled, err := r.recordDiscoveryCredentialSourceScope(ctx, nc, config, status); handled {
		if err != nil {
			return source, false, "DiscoveryCredentialScopeUnavailable", "Credential discovery source and projection must validate before mutation"
		}
		return source, true, "DiscoveryCredentialScopeRecorded", "Validated non-secret credential discovery scope recorded before mutation"
	}
	if source, handled, err := r.recordDiscoverySourceScope(ctx, nc, config, status); handled {
		if err != nil {
			return source, false, "DiscoveryScopeUnavailable", "Pure discovery source must be available and validated before mutation"
		}
		return source, true, "DiscoveryScopeRecorded", "Validated non-secret discovery scope recorded before mutation"
	}
	if supportedConfigurationApply(config) {
		return r.evaluateConfigurationApply(ctx, nc, config, status)
	}
	if !supportedConfigReport(config) {
		return nil, false, "UnsupportedConfiguration", "Selected controller configuration mode, scope, retention policy or credential input is unsupported"
	}
	source, err := loadControllerSource(ctx, r.a.statusReader, config)
	if err != nil {
		return nil, false, "SourceUnavailable", "Cannot read controller intent source"
	}
	observationCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	plans, err := r.a.client.compileConfigReportsWithAssets(source.raw, config.Spec.ManagedSections, source.templateFiles, source.floorImages)
	if err != nil {
		return source, false, "UnsupportedIntent", "Controller intent is invalid, unresolved or contains unsupported fields"
	}
	var drift []configapi.NetworkControllerDriftEntry
	var sections []configapi.NetworkControllerSectionStatus
	notes := ""
	incomplete := false
	for _, plan := range plans {
		differences, err := plan.observe(observationCtx)
		if err != nil {
			return source, true, "ObservationFailed", "Selected controller configuration could not be read completely"
		}
		if plan.observationNote != nil {
			plan.note = plan.observationNote()
		}
		state := configapi.NetworkControllerSectionStateInSync
		if plan.incompleteObservation {
			incomplete = true
			state = configapi.NetworkControllerSectionStateSkipped
		}
		if len(differences) > 0 {
			state = configapi.NetworkControllerSectionStateDrifted
		}
		drift = append(drift, differences...)
		sections = append(sections, configapi.NetworkControllerSectionStatus{Name: plan.name, State: state, ResourceCount: int64(plan.count), Message: plan.note})
		if plan.note != "" {
			notes += "; " + plan.note
		}
	}
	now := metav1.Now()
	status.LastControllerCheck = &now
	status.ControllerAPIVersion = "v1"
	status.Phase = configapi.NetworkControllerConfigPhaseInSync
	reason, message := "ReportComplete", "Requested configuration attributes match; no changes were applied"
	if incomplete {
		status.Phase = configapi.NetworkControllerConfigPhaseWaiting
		reason, message = "ObservationIncomplete", "Observable configuration attributes match; some declared resources cannot be independently observed"
	}
	if len(drift) > 0 {
		status.Phase = configapi.NetworkControllerConfigPhaseDrifted
		reason, message = "DriftDetected", "Requested configuration attributes differ; no changes were applied"
	}
	if len(drift) > 50 {
		drift = drift[:50]
		message = "Configuration drift detected; summary limited to 50 entries; no changes were applied"
	}
	status.Drift = drift
	status.SectionStatus = sections
	return source, true, reason, message + notes
}

func supportedConfigReport(c *configapi.NetworkControllerConfig) bool {
	s := c.Spec
	if len(s.ManagedSections) == 0 {
		return false
	}
	for _, section := range s.ManagedSections {
		if !reportSectionSupported(section) {
			return false
		}
	}
	return (s.Mode == "" || s.Mode == configapi.NetworkControllerApplyModeReport) && (s.Scope == "" || s.Scope == "global") && len(s.SecretRefs) == 0 && s.ModelSource.SchemaDigest == "" && (s.PrunePolicy == "" || s.PrunePolicy == configapi.NetworkControllerRetentionPolicyRetain) && (s.DeletionPolicy == "" || s.DeletionPolicy == configapi.NetworkControllerRetentionPolicyRetain) && len(c.Status.Tasks) == 0
}

func loadControllerSource(ctx context.Context, r ctrlclient.Reader, c *configapi.NetworkControllerConfig) (*configSourceSnapshot, error) {
	source := &configSourceSnapshot{deletion: featureDeletionContext(c, false)}
	if c.Spec.Source.Inline != nil {
		source.raw = append([]byte(nil), c.Spec.Source.Inline.Raw...)
		if err := bindSiteSourceSelection(source, c); err != nil {
			return nil, errors.New("site source selection unavailable")
		}
		return source, nil
	}
	ref := c.Spec.Source.ConfigMapRef
	if ref == nil {
		return nil, errors.New("source missing")
	}
	source.key = types.NamespacedName{Namespace: c.Namespace, Name: ref.Name}
	var cm corev1.ConfigMap
	if err := r.Get(ctx, source.key, &cm); err != nil {
		return nil, err
	}
	text, ok := cm.Data[ref.Key]
	if !ok || !cm.DeletionTimestamp.IsZero() {
		return nil, errors.New("source key unavailable")
	}
	source.raw = []byte(text)
	if slices.Contains(c.Spec.ManagedSections, "templates") {
		if ref.Key == nacmodel.TemplateAssetsKey {
			return nil, errors.New("model and template asset keys must differ")
		}
		if _, exists := cm.BinaryData[nacmodel.TemplateAssetsKey]; exists {
			return nil, errors.New("template assets require text ConfigMap data")
		}
		if assets, present := cm.Data[nacmodel.TemplateAssetsKey]; present {
			var err error
			source.templateFiles, err = nacmodel.ParseTemplateAssets([]byte(assets))
			if err != nil {
				return nil, err
			}
		}
	}
	// Bulk inventory may include sites as unmanaged dependency context. Keep
	// its optional floor assets in the same ConfigMap UID/revision snapshot.
	if slices.Contains(c.Spec.ManagedSections, "sites") || wiredBulkSourceHasSiteContext(c, source.raw) {
		var err error
		source.floorImages, err = loadFloorImageAssets(&cm, ref.Key)
		if err != nil {
			return nil, err
		}
	}
	if err := bindSiteSourceSelection(source, c); err != nil {
		return nil, errors.New("site source selection unavailable")
	}
	source.uid = cm.UID
	source.version = cm.ResourceVersion
	return source, nil
}

// ConfigMap revisions affect intent without changing the referring CR generation.
func (a *adapter) configsForSource(ctx context.Context, obj ctrlclient.Object) []ctrl.Request {
	if obj.GetNamespace() != a.key.Namespace {
		return nil
	}
	var configs configapi.NetworkControllerConfigList
	if err := a.statusReader.List(ctx, &configs, ctrlclient.InNamespace(a.key.Namespace)); err != nil {
		ctrl.LoggerFrom(ctx).Error(err, "Cannot list controller config source dependents")
		return nil
	}
	var requests []ctrl.Request
	for _, config := range configs.Items {
		ref := config.Spec.Source.ConfigMapRef
		if config.Spec.ControllerRef.Name == a.key.Name && ref != nil && ref.Name == obj.GetName() {
			requests = append(requests, ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(&config)})
		}
	}
	return requests
}
