// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"strings"
	"time"
)

func discoveryLogicalResource(resource string) string {
	if discoveryFinalizeSelected(resource) {
		return discoveryFinalizeCanonical(resource)
	}
	return strings.TrimPrefix(resource, "discoveries/")
}
func ownedDiscoverySelected(resource string) bool {
	if discoveryFinalizeSelected(resource) {
		parts := discoveryFinalizeParts(resource)
		// Malformed envelopes remain reserved, but typed credential cleanup
		// belongs to its own receipt/capacity checks, not ordinary discovery.
		return parts == nil || strings.HasPrefix(parts[3], ownedDiscoveryPrefix)
	}
	return strings.HasPrefix(discoveryLogicalResource(resource), ownedDiscoveryPrefix)
}
func localDiscoveryOperation(op *cvk.NetworkControllerConfigurationOperation) *cvk.NetworkControllerConfigurationOperation {
	local := op.DeepCopy()
	local.Section = "inventory"
	local.Resource = discoveryLogicalResource(local.Resource)
	return local
}

// Compose resource callbacks with the preexisting plan; retain original journal
// paths and delegate unrelated completion/recovery rather than replacing it.
func (a *adapter) bindOwnedDiscoveryExecutor(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan, e *ownedDiscoveryExecutor, prefix string) {
	e.bindRetiredHistory(nc, string(config.UID))
	prior := *plan
	ours := func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && op.Section == plan.section && (strings.HasPrefix(op.Resource, prefix+ownedDiscoveryPrefix) || prefix == "" && discoveryFinalizeSelected(op.Resource) && ownedDiscoveryOperation(op))
	}
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return ours(op) || prior.recordCompletion != nil && (prior.receiptOperation == nil || prior.receiptOperation(op))
	}
	if prior.observeCompletion != nil {
		plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
			if ours(op) {
				return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
			}
			return prior.observeCompletion(ctx, op, now)
		}
	}
	e.submissionGuard = func(ctx context.Context, resource, action string, identity configurationResourceIdentity) error {
		return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, plan.section, prefix+resource, action, identity, ownedDiscoveryOperation, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
			if !discoveryDispatchReceiptUnchanged(e.receipts, state.ActionApplications, resource) {
				return errConfigurationOwned
			}
			fresh, err := a.currentController(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(e.retired, discoveryRetiredReceiptKeys(fresh, string(config.UID))) {
				return errConfigurationOwned
			}
			return ownedDiscoveryCapacity(state, op, e.retired)
		})
	}
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if !ours(op) {
			return prior.canObserveUnknownTask(op)
		}
		local := localDiscoveryOperation(op)
		if !ownedDiscoveryOperation(op) || op.Phase != "Unknown" || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.ConfigUID != string(config.UID) {
			return false
		}
		if e.retainedObservation && op.Action == "Create" {
			mask, _, valid := ownedDiscoveryFingerprintParts(op.Resource)
			return valid && mask != 0 && source.discoveryScope != nil && op.SourceUID == string(source.uid) && op.SourceVersion == source.version
		}
		for _, want := range e.base.desired {
			if strings.HasPrefix(local.Resource, ownedDiscoveryPrefix+discoveryPublicDigest(want.name)+":") {
				return true
			}
		}
		if e.prune && op.Action == "Delete" && strings.HasSuffix(op.Resource, discoveryPruneSuffix) {
			r, err := e.receiptByHash(discoveryOperationNameHash(op.Resource))
			return err == nil && r != nil && r.Operation.Action == "Create" && r.Operation.Resource == strings.TrimSuffix(local.Resource, discoveryPruneSuffix) && r.Resource == ownedDiscoveryReceiptPrefix+discoveryOperationNameHash(op.Resource)+":"+op.TargetID
		}
		return false
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if !ours(op) {
			if prior.recordCompletion == nil {
				return errConfigurationTransition
			}
			return prior.recordCompletion(ctx, op)
		}
		if !ownedDiscoveryOperation(op) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || !validAPIID(op.TaskID) || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || !discoveryOperationGenerationMatches(config, source, op) || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
			return errConfigurationStale
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.UID != nc.UID || current.Generation != nc.Generation || current.Spec.Paused || !current.DeletionTimestamp.IsZero() || current.Status.Configuration == nil || !reflect.DeepEqual(current.Status.Configuration.Operation, op) || op.Phase != "Verifying" || op.ConfigUID != string(config.UID) {
			return errConfigurationStale
		}
		done, err := a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		id, done, err := e.verifyOwned(ctx, op)
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		state := current.Status.Configuration
		if err := configurationOwnersMatch(state, config); err != nil {
			return err
		}
		if !reflect.DeepEqual(e.retired, discoveryRetiredReceiptKeys(current, string(config.UID))) {
			return errConfigurationOwned
		}
		if err := ownedDiscoveryCapacity(state, op, e.retired); err != nil {
			return err
		}
		receiptPrefix := ownedDiscoveryReceiptPrefix + discoveryOperationNameHash(op.Resource) + ":"
		retained := state.ActionApplications[:0]
		for _, r := range state.ActionApplications {
			if !strings.HasPrefix(r.Resource, receiptPrefix) || e.retired[r.Resource] && r.Operation.ConfigUID != op.ConfigUID {
				retained = append(retained, r)
			}
		}
		state.ActionApplications = retained
		state.Operation.Phase = "Verified"
		stamp := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		if op.SubmittedAt.After(stamp.Time) {
			stamp = *op.SubmittedAt.DeepCopy()
		}
		state.ActionApplications = append(state.ActionApplications, cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: receiptPrefix + id, Operation: *state.Operation.DeepCopy(), VerifiedAt: &stamp})
		return a.persistConfigurationActionReceipt(ctx, current)
	}
}

// Existing groups supply ordering, whole-model preflight and one common journal.
// No generic group or physical-device state machine is changed.
func (a *adapter) bindDiscoveryOwnership(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if nc == nil || config == nil || source == nil || plan == nil {
		return errConfigurationTransition
	}
	if len(config.Spec.SecretRefs) != 0 || !(len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "inventory" || combinedTagSections(config.Spec.ManagedSections)) {
		return nil
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		resource := nc.Status.Configuration.Operation.Resource
		if strings.HasPrefix(resource, "discovery:") || strings.HasPrefix(resource, "discoveries/discovery:") {
			return nil
		}
	}
	var walk func(*configurationExecutor, string) error
	walk = func(slot *configurationExecutor, prefix string) error {
		switch e := (*slot).(type) {
		case *ownedDiscoveryExecutor:
			return nil // Already bound by the pure compiler.
		case *configurationGroup:
			for i := range e.parts {
				part := &e.parts[i]
				if err := walk(&part.executor, prefix+part.name+"/"); err != nil {
					return err
				}
			}
		case *discoveryExecutor:
			if !(plan.section == "inventory" && prefix == "" || prefix == "discoveries/" && (plan.section == "inventory" || plan.section == "templates")) {
				return nil // Preserve unrelated nested/device composition until explicitly qualified.
			}
			if config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
				return errConfigurationTransition
			}
			owned := &ownedDiscoveryExecutor{base: e, owner: string(config.UID)}
			if nc.Status.Configuration != nil {
				owned.receipts = nc.Status.Configuration.DeepCopy().ActionApplications
			}
			*slot = owned
			a.bindOwnedDiscoveryExecutor(nc, config, source, plan, owned, prefix)
		}
		return nil
	}
	return walk(&plan.executor, "")
}
