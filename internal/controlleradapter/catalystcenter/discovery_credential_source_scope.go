// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const discoveryCredentialSourceCondition = "DiscoveryCredentialSourceScope"
const discoveryCredentialSourceReason = "CredentialDiscoverySource"

// The existing scope envelope and idle-controller publication guard are reused.
// Validate the complete resolved source and projection before retaining metadata;
// neither the source body nor credentials are copied into the status attestation.
func (r *configReconciler) recordDiscoveryCredentialSourceScope(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, status *configapi.NetworkControllerConfigStatus) (*configSourceSnapshot, bool, error) {
	if !supportedConfigurationApply(c) || !discoveryCredentialLifecycleShape(c) || len(c.Spec.SecretRefs) == 0 && !discoveryCredentialHistoryOwner(nc, c) && !discoveryNoAuthInitialSpecShape(c) || !c.DeletionTimestamp.IsZero() || nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, false, nil
	}
	source, err := loadControllerSource(ctx, r.a.statusReader, c)
	if err != nil {
		if len(c.Spec.SecretRefs) == 0 && !discoveryCredentialHistoryOwner(nc, c) {
			return nil, false, nil
		}
		return nil, true, err
	}
	if !discoveryCredentialPlanSelected(nc, c, source) {
		return nil, false, nil
	}
	if _, err := r.a.compileDiscoveryCredentialApply(nc, c, source); err != nil {
		return source, true, err
	}
	proof := discoveryScopeIdentity(nc, c)
	proof.SourceUID = string(source.uid)
	proof.SourceVersion = source.version
	proof.ModelDigest = featureIdentityDigest(source.raw)
	raw, _ := json.Marshal(proof)
	old := meta.FindStatusCondition(c.Status.Conditions, discoveryCredentialSourceCondition)
	if old != nil && old.Status == metav1.ConditionTrue && old.ObservedGeneration == c.Generation && old.Reason == discoveryCredentialSourceReason && old.Message == string(raw) {
		return source, false, nil
	}
	current, err := r.a.currentController(ctx)
	if err != nil {
		return source, true, err
	}
	if current.UID != nc.UID || current.Generation != nc.Generation || current.ResourceVersion != nc.ResourceVersion || current.Status.Configuration != nil && current.Status.Configuration.Operation != nil {
		return source, true, errConfigurationStale
	}
	source.discoveryScopeControllerVersion = nc.ResourceVersion
	status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	meta.SetStatusCondition(&status.Conditions, condition(discoveryCredentialSourceCondition, true, discoveryCredentialSourceReason, string(raw), c.Generation))
	return source, true, nil
}

// Authorize observation/publication of acknowledged original discovery work only.
// This is not a replacement source snapshot or a native submission capability.
func discoveryCredentialScopeForOperation(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) *discoverySourceScope {
	return discoveryCredentialScopeForContext(nc, c, op, false)
}

func discoveryCredentialScopeForContext(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation, deleting bool) *discoverySourceScope {
	if nc == nil || c == nil || op == nil || !supportedConfigurationApply(c) || !discoveryCredentialLifecycleShape(c) || !deleting && !c.DeletionTimestamp.IsZero() || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || c.Namespace != nc.Namespace || c.Spec.ControllerRef.Name != nc.Name || op.ConfigUID != string(c.UID) || op.ConfigName != c.Name || (!deleting && op.ConfigGeneration != c.Generation || deleting && (op.ConfigGeneration < 1 || op.ConfigGeneration != c.Generation && op.ConfigGeneration+1 != c.Generation)) || op.ControllerGeneration != nc.Generation {
		return nil
	}
	if deleting && !discoveryCredentialCleanupObservationShape(c) {
		return nil
	}
	if !discoveryCredentialRetainedRecoveryOperation(op) {
		return nil
	}
	if data, ok := discoveryCredentialRetirementParts(op); ok && data.prune && c.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete {
		return nil
	}
	if !discoveryCredentialRetainedContract(nc, c, op) {
		return nil
	}
	cond := meta.FindStatusCondition(c.Status.Conditions, discoveryCredentialSourceCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != discoveryCredentialSourceReason || cond.ObservedGeneration != op.ConfigGeneration || cond.LastTransitionTime.IsZero() {
		return nil
	}
	if deleting && op.Phase == "Prepared" {
		if op.SubmittedAt != nil || op.TaskID != "" || cond.LastTransitionTime.After(c.DeletionTimestamp.Add(time.Second)) {
			return nil
		}
	} else if op.SubmittedAt == nil || op.SubmittedAt.IsZero() || cond.LastTransitionTime.After(op.SubmittedAt.Time) || deleting && !op.SubmittedAt.Before(&metav1.Time{Time: c.DeletionTimestamp.Add(time.Second)}) {
		return nil
	}
	var proof discoverySourceScope
	if json.Unmarshal([]byte(cond.Message), &proof) != nil || !lowerDiscoveryDigest(proof.ModelDigest) {
		return nil
	}
	if data, ok := discoveryCredentialRetirementParts(op); ok && data.prune && data.scope != proof.ModelDigest {
		return nil
	}
	expected := discoveryScopeIdentity(nc, c)
	expected.ConfigGeneration = op.ConfigGeneration
	expected.SourceUID = op.SourceUID
	expected.SourceVersion = op.SourceVersion
	expected.ModelDigest = proof.ModelDigest
	if c.Spec.Source.Inline != nil {
		if op.SourceUID != "" || op.SourceVersion != "" || proof.ModelDigest != featureIdentityDigest(c.Spec.Source.Inline.Raw) {
			return nil
		}
	} else if c.Spec.Source.ConfigMapRef == nil || op.SourceUID == "" || op.SourceVersion == "" {
		return nil
	}
	raw, _ := json.Marshal(expected)
	if string(raw) != cond.Message {
		return nil
	}
	return &proof
}

// Reuse the complete common metadata/descriptor/Secret-binding validation
// without claiming that projected bytes still exist. The frozen attestation and operation bindings authorize
// completion only; normal dispatch keeps the original full contract gate.
func discoveryCredentialRetainedContract(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) bool {
	if controlleradapter.ValidateConfigMetadataContract(nc, c) != nil {
		return false
	}
	for _, version := range op.CredentialVersions {
		matched := false
		for _, ref := range c.Spec.SecretRefs {
			if ref.Section != "inventory" || ref.Path != version.Path || ref.Source != version.SourceAlias || ref.Use != "" && ref.Use != "Value" {
				continue
			}
			for _, source := range nc.Spec.IntentSecretSources {
				if source.Alias == ref.Source && source.Name == version.SecretName && source.Key == version.SecretKey {
					matched = true
				}
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// Bind idle planning to the exact source validated by the preceding scope step.
// ConfigMap content cannot change at a retained UID/RV; compare those identities
// before hashing the body, so a new unvalidated source is never adopted here.
func discoveryCredentialAttestationMatchesSource(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) bool {
	if nc == nil || c == nil || source == nil {
		return false
	}
	cond := meta.FindStatusCondition(c.Status.Conditions, discoveryCredentialSourceCondition)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != discoveryCredentialSourceReason || cond.ObservedGeneration != c.Generation {
		return false
	}
	var proof discoverySourceScope
	if json.Unmarshal([]byte(cond.Message), &proof) != nil || proof.ControllerUID != string(nc.UID) || proof.ControllerGeneration != nc.Generation || proof.ConfigUID != string(c.UID) || proof.ConfigGeneration != c.Generation || proof.SourceUID != string(source.uid) || proof.SourceVersion != source.version {
		return false
	}
	expected := discoveryScopeIdentity(nc, c)
	expected.SourceUID = string(source.uid)
	expected.SourceVersion = source.version
	expected.ModelDigest = featureIdentityDigest(source.raw)
	raw, _ := json.Marshal(expected)
	return string(raw) == cond.Message
}

func (a *adapter) checkDiscoveryCredentialRetainedAuthority(ctx context.Context, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) (*cvk.NetworkController, error) {
	var fresh configapi.NetworkControllerConfig
	if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(c), &fresh); err != nil {
		return nil, err
	}
	if fresh.UID != c.UID || fresh.Generation != c.Generation || fresh.ResourceVersion != c.ResourceVersion || !reflect.DeepEqual(fresh.Spec, c.Spec) {
		return nil, errConfigurationStale
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if discoveryCredentialScopeForOperation(nc, &fresh, op) == nil || nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) || configurationOwnersMatch(nc.Status.Configuration, &fresh) != nil {
		return nil, errConfigurationOwned
	}
	if discoveryCredentialCustodyRequired(nc, string(c.UID)) {
		if _, err := discoveryCredentialCustodyForOperation(nc, &fresh, op); err != nil {
			return nil, err
		}
	}
	return nc, nil
}

func (a *adapter) recordRetainedDiscoveryCredentialCreate(ctx context.Context, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) error {
	if op == nil || op.Phase != "Verifying" {
		return errConfigurationTransition
	}
	if _, err := a.checkDiscoveryCredentialRetainedAuthority(ctx, c, op); err != nil {
		return err
	}
	observation, err := a.observeRetainedDiscoveryCredentialCreate(ctx, op)
	if err != nil {
		return err
	}
	nc, err := a.checkDiscoveryCredentialRetainedAuthority(ctx, c, op)
	if err != nil {
		return err
	}
	return a.publishObservedDiscoveryCredentialCreate(ctx, nc, op, observation)
}

// Recover original acknowledged work before loading a changed/missing source.
// All successful paths stay Waiting; a retained receipt is not current InSync.
// No Prepared/Submitting/taskless work is replayed or given a synthetic source.
func (r *configReconciler) evaluateRetainedDiscoveryCredential(ctx context.Context, nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, status *configapi.NetworkControllerConfigStatus) (bool, string, string) {
	if c.Spec.Mode != configapi.NetworkControllerApplyModeApply || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return false, "", ""
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	if op.ConfigUID != string(c.UID) || op.ConfigName != c.Name {
		return false, "", ""
	}
	if !discoveryCredentialRetainedRecoveryOperation(op) {
		return false, "", ""
	}
	if op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying" && op.Phase != "Verified" {
		return false, "", ""
	}
	// Prefer the unchanged live-source path whenever its projection is valid.
	source, err := loadControllerSource(ctx, r.a.statusReader, c)
	if err == nil && string(source.uid) == op.SourceUID && source.version == op.SourceVersion {
		if _, err := r.a.prepareDiscoveryCredentialProjection(nc, c, source); err == nil {
			return false, "", ""
		}
	}
	status.Phase = configapi.NetworkControllerConfigPhaseWaiting
	status.SectionStatus = []configapi.NetworkControllerSectionStatus{{Name: "inventory", State: configapi.NetworkControllerSectionStateApplying}}
	reason, message := "DiscoveryCredentialRecoveryPending", "Original acknowledged discovery remains fenced until retained source, task, UUID and native state are verified"
	if _, err := r.a.checkDiscoveryCredentialRetainedAuthority(ctx, c, op); err != nil {
		return true, reason, message
	}
	switch op.Phase {
	case "Accepted", "Unknown":
		if !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) {
			return true, reason, message
		}
		done, err := r.a.observeDiscoveryDeletionTask(ctx, c, op)
		if err != nil {
			var failure *configurationTaskFailure
			if errors.As(err, &failure) {
				status.Phase = configapi.NetworkControllerConfigPhaseFailed
				return true, "ControllerTaskFailed", "Original discovery task failed; its journal and ownership remain fenced"
			}
			return true, reason, message
		}
		if !done {
			return true, reason, message
		}
		if _, err := r.a.checkDiscoveryCredentialRetainedAuthority(ctx, c, op); err != nil {
			return true, reason, message
		}
		if err := r.a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
			return true, reason, message
		}
	case "Verifying":
		var err error
		if discoveryCredentialRetirementSelected(op.Resource) {
			err = r.a.recordRetainedDiscoveryCredentialRetirement(ctx, c, op)
		} else {
			err = r.a.recordRetainedDiscoveryCredentialCreate(ctx, c, op)
		}
		if err != nil {
			return true, reason, message
		}
	case "Verified":
		valid := discoveryCredentialReceiptPairPresent(nc.Status.Configuration, op)
		if discoveryCredentialRetirementSelected(op.Resource) {
			valid = discoveryCredentialRetirementReceiptPresent(nc.Status.Configuration, op)
		}
		if !valid || !discoveryCredentialHistoryValid(nc.Status.Configuration, op.ConfigUID) {
			return true, reason, message
		}
		if err := r.a.finishConfiguration(ctx, op.ID); err != nil {
			return true, reason, message
		}
	}
	return true, "DiscoveryCredentialOriginalWorkRecovered", "Original discovery work advanced using retained scope; current intent still requires a valid source and projection"
}

func discoveryCredentialRetainedRecoveryOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	// Deletion-bound work cannot enter the active-config recovery bridge.
	if op != nil && discoveryFinalizeSelected(op.Resource) {
		return false
	}
	if _, ok := discoveryCredentialRetirementParts(op); ok {
		return true
	}
	if op == nil {
		return false
	}
	if _, ok := discoveryCredentialRetainedEffect(op.Resource); !ok {
		return false
	}
	_, ok := discoveryCredentialOperationInput(op)
	return ok
}

func (a *adapter) recordRetainedDiscoveryCredentialRetirement(ctx context.Context, c *configapi.NetworkControllerConfig, op *cvk.NetworkControllerConfigurationOperation) error {
	if op == nil || op.Phase != "Verifying" {
		return errConfigurationTransition
	}
	if _, err := a.checkDiscoveryCredentialRetainedAuthority(ctx, c, op); err != nil {
		return err
	}
	observed, err := a.observeRetainedDiscoveryCredentialRetirement(ctx, op)
	if err != nil {
		return err
	}
	current, err := a.checkDiscoveryCredentialRetainedAuthority(ctx, c, op)
	if err != nil {
		return err
	}
	if current.ResourceVersion != observed.ResourceVersion || !reflect.DeepEqual(current.Status.Configuration, observed.Status.Configuration) {
		return errConfigurationStale
	}
	return a.publishObservedDiscoveryCredentialRetirement(ctx, current, op)
}
