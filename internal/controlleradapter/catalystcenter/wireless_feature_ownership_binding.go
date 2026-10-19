// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (a *adapter) bindFeatureCreateOwnership(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if plan.section != "feature_templates" {
		return nil
	}
	wrapper, ok := plan.executor.(*wirelessConfiguration)
	if !ok {
		return errConfigurationTransition
	}
	e := &featureOwnedCreateExecutor{base: plan.executor, client: a.client, owner: string(config.UID), ownerName: config.Name, bindings: map[string]featureCreateBinding{}}
	e.configGeneration, e.controllerGeneration = config.Generation, nc.Generation
	if source != nil {
		e.sourceUID, e.sourceVersion = string(source.uid), source.version
	}
	if nc.Status.Configuration != nil {
		e.receipts = nc.Status.Configuration.DeepCopy().ActionApplications
	}
	bindGuard := func(legacy string, b featureCreateBinding) func(context.Context, string, string, configurationResourceIdentity) error {
		return func(ctx context.Context, local, action string, identity configurationResourceIdentity) error {
			resource := legacy
			valid := func(op *cvk.NetworkControllerConfigurationOperation) bool {
				return op != nil && op.Section == "feature_templates" && op.Action == "Update" && op.Resource == legacy && op.TargetID == identity.target && op.ParentID == "" && op.TargetSerial == "" && op.PhysicalGroup == nil && op.DependencyVersions == nil && op.CancellationRequestedAt == nil && op.CreatedResourceID == "" && op.PreparedAt == nil && len(op.SSIDCredentialHistory) == 0 && len(op.CredentialVersions) == 0
			}
			if action == "Create" {
				resource = featureRecreateIntent(b, &cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
				valid = validFeatureCreateOperation
			} else if records, err := featureCreatedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}); err == nil {
				if _, owned := records[b.key()]; owned {
					resource = featureUpdatePrefix + strings.TrimPrefix(b.resource, featureCreatePrefix)
					valid = func(op *cvk.NetworkControllerConfigurationOperation) bool { _, ok := featureUpdateKey(op); return ok }
				}
			}
			if action == "Create" && featureRecreateSelected(resource) {
				if err := e.recreationAbsent(ctx, b); err != nil {
					return err
				}
			}
			return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, plan.section, resource, action, identity, valid, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
				before, err := featureCreatedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
				if err != nil {
					return err
				}
				current, err := featureCreatedRecords(state)
				if err != nil {
					return err
				}
				old, oldExists := before[b.key()]
				now, nowExists := current[b.key()]
				if oldExists != nowExists || oldExists && !reflect.DeepEqual(old, now) {
					return errConfigurationOwned
				}
				if oldExists {
					id, _ := featureCreatedTarget(old)
					if old.Operation.ConfigUID != e.owner || old.Operation.ConfigName != e.ownerName || (identity.target != id && !featureRecreateSelected(resource)) {
						return errConfigurationOwned
					}
				}
				beforeApplied, err := featureAppliedRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts})
				if err != nil {
					return err
				}
				currentApplied, err := featureAppliedRecords(state)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(beforeApplied[b.key()], currentApplied[b.key()]) {
					return errConfigurationOwned
				}
				if action == "Create" {
					return featureCreateCapacity(state, op)
				}
				if featureUpdateSelected(op.Resource) {
					return featureUpdateCapacity(state, op)
				}
				return nil
			})
		}
	}
	bind := func(executor configurationExecutor, prefix string) error {
		switch child := executor.(type) {
		case *wirelessCleanAirExecutor:
			for _, want := range child.desired {
				legacy := prefix + "cleanair:" + want.Name
				b := featureCreateBinding{kind: wirelessCleanAirKind, name: want.Name, legacy: legacy, resource: featureCreatePrefix + "ca:" + featureIdentityDigest(want.Name) + ":" + featureIdentityDigest(want)}
				b.verifyState = func(ctx context.Context, ref wirelessFeatureTaskReference) (string, string, bool, error) {
					return verifyWirelessFeatureObservedResult(ctx, a.client, wirelessCleanAirKind, ref, want, a.client.wirelessCleanAirReadState, wirelessCleanAirMatches, time.Now())
				}
				b.verify = func(ctx context.Context, ref wirelessFeatureTaskReference) (string, bool, error) {
					id, _, done, err := b.verifyState(ctx, ref)
					return id, done, err
				}
				e.bindings[legacy] = b
			}
			child.submissionGuard = func(ctx context.Context, local, action string, identity configurationResourceIdentity) error {
				legacy := prefix + local
				b, ok := e.bindings[legacy]
				if !ok {
					return errConfigurationTransition
				}
				return bindGuard(legacy, b)(ctx, local, action, identity)
			}
		case *wirelessFRAExecutor:
			for _, want := range child.desired {
				legacy := prefix + "rrm-fra:" + want.Name
				b := featureCreateBinding{kind: wirelessFRAKind, name: want.Name, legacy: legacy, resource: featureCreatePrefix + "fra:" + featureIdentityDigest(want.Name) + ":" + featureIdentityDigest(want)}
				b.verifyState = func(ctx context.Context, ref wirelessFeatureTaskReference) (string, string, bool, error) {
					return verifyWirelessFeatureObservedResult(ctx, a.client, wirelessFRAKind, ref, want, a.client.wirelessFRAReadState, wirelessFRAMatches, time.Now())
				}
				b.verify = func(ctx context.Context, ref wirelessFeatureTaskReference) (string, bool, error) {
					id, _, done, err := b.verifyState(ctx, ref)
					return id, done, err
				}
				e.bindings[legacy] = b
			}
			child.submissionGuard = func(ctx context.Context, local, action string, identity configurationResourceIdentity) error {
				legacy := prefix + local
				b, ok := e.bindings[legacy]
				if !ok {
					return errConfigurationTransition
				}
				return bindGuard(legacy, b)(ctx, local, action, identity)
			}
		default:
			return errConfigurationTransition
		}
		return nil
	}
	// Retained legacy Creates keep their original verification/recovery semantics.
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		if op.Section == "feature_templates" && (op.Action == "Create" || op.Action == "Update") && featureLegacyCreateResource(op.Resource) {
			return nil
		}
	}
	switch child := wrapper.configurationExecutor.(type) {
	case *wirelessFeatureGroup:
		for _, part := range child.parts {
			if err := bind(part.executor, part.name+"/"); err != nil {
				return err
			}
		}
	default:
		if err := bind(child, ""); err != nil {
			return err
		}
	}
	prior := *plan
	find := func(op *cvk.NetworkControllerConfigurationOperation) (featureCreateBinding, bool) {
		_, update := featureUpdateKey(op)
		if !validFeatureCreateOperation(op) && !update {
			return featureCreateBinding{}, false
		}
		for _, b := range e.bindings {
			if b.resource == featureCreateCanonical(op.Resource) || update && featureUpdatePrefix+strings.TrimPrefix(b.resource, featureCreatePrefix) == op.Resource {
				return b, true
			}
		}
		return featureCreateBinding{}, false
	}
	plan.executor = e
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && (featureCreateSelected(op.Resource) || featureUpdateSelected(op.Resource)) || prior.recordCompletion != nil && (prior.receiptOperation == nil || prior.receiptOperation(op))
	}
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if op != nil && (featureCreateSelected(op.Resource) || featureUpdateSelected(op.Resource)) {
			_, ok := find(op)
			return ok && op.Phase == "Unknown" && validAPIID(op.TaskID) && op.SubmittedAt != nil && !op.SubmittedAt.IsZero() && op.ConfigUID == e.owner
		}
		return prior.canObserveUnknownTask(op)
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if !featureCreateSelected(op.Resource) && !featureUpdateSelected(op.Resource) {
			if prior.recordCompletion == nil {
				return errConfigurationTransition
			}
			return prior.recordCompletion(ctx, op)
		}
		b, ok := find(op)
		if !ok || op.Phase != "Verifying" || op.SubmittedAt == nil || !validAPIID(op.TaskID) || op.ConfigUID != e.owner || op.ConfigName != config.Name || !featureOperationGenerationMatches(config, source, op) || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
			return errConfigurationStale
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.UID != nc.UID || current.Generation != nc.Generation || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) {
			return errConfigurationStale
		}
		if err = configurationOwnersMatch(current.Status.Configuration, config); err != nil {
			return err
		}
		capacity := featureCreateCapacity
		if featureUpdateSelected(op.Resource) {
			capacity = featureUpdateCapacity
		}
		if err = capacity(current.Status.Configuration, op); err != nil {
			return err
		}
		id, fingerprint, done, err := b.verifyState(ctx, wirelessFeatureTaskReference{ID: op.TaskID, Name: b.name, Action: op.Action, Target: op.TargetID, Submitted: op.SubmittedAt.Time})
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		if featureRecreateSelected(op.Resource) {
			if err := featureRecreatedIDUnused(current.Status.Configuration, b.key(), id); err != nil {
				return err
			}
		}
		if !validWirelessFeatureFingerprint(fingerprint) {
			return errInvalidResponse
		}
		if err = a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		if featureRecreateSelected(op.Resource) {
			if err := archiveFeatureCohort(current.Status.Configuration, op); err != nil {
				return err
			}
		}
		current.Status.Configuration.Operation.Phase = "Verified"
		now := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		if op.SubmittedAt.After(now.Time) {
			now = *op.SubmittedAt.DeepCopy()
		}
		resource := featureCreatedPrefix + b.key() + ":" + id + ":" + fingerprint
		if featureUpdateSelected(op.Resource) {
			resource = featureAppliedPrefix + b.key() + ":" + id + ":" + fingerprint
			retained := current.Status.Configuration.ActionApplications[:0]
			for _, r := range current.Status.Configuration.ActionApplications {
				key, _ := featureUpdateKey(&r.Operation)
				if featureRetiredSelected(r.Resource) || !featureUpdateSelected(r.Operation.Resource) || key != b.key() {
					retained = append(retained, r)
				}
			}
			current.Status.Configuration.ActionApplications = retained
		}
		current.Status.Configuration.ActionApplications = append(current.Status.Configuration.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: resource, Operation: *current.Status.Configuration.Operation.DeepCopy(), VerifiedAt: &now})
		if err := a.persistConfigurationActionReceipt(ctx, current); err != nil {
			return err
		}
		if featureRecreateSelected(op.Resource) {
			stored, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			if stored.Status.Configuration == nil || !featureCreatedReceiptPresent(stored.Status.Configuration, stored.Status.Configuration.Operation) {
				return errConfigurationOwned
			}
		}
		return nil
	}
	return nil
}

// Only the explicit new protocol participates; legacy prefixes never adopt a
// receipt solely from matching native metadata.
func featureLegacyCreateResource(resource string) bool {
	return strings.HasPrefix(resource, "cleanair:") || strings.HasPrefix(resource, "rrm-fra:") || strings.HasPrefix(resource, "cleanair/cleanair:") || strings.HasPrefix(resource, "rrm_fra/rrm-fra:")
}
