// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/hex"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"reflect"
	"strings"
	"time"
)

// Receipt suppression is not compliance or mutation admission. ALWAYS means
// once per explicit CVK input revision, never once per reconciliation. ON_CHANGE
// compares private target-input MACs across revisions when explicitly enabled.
// Legacy records without comparable inputs remain fenced rather than guessed.
func templateDeploymentAlreadyApplied(state *cvk.NetworkControllerConfigurationStatus, desired *cvk.NetworkControllerConfigurationOperation, policy string, now time.Time) (bool, error) {
	if desired == nil || !validTemplateRedeploy(policy) || desired.ConfigUID == "" || desired.ConfigName == "" || desired.ConfigGeneration < 1 || desired.ControllerGeneration < 1 || desired.Section != "inventory" || desired.Action != "Update" || !validAPIID(desired.TargetID) || !validAPIID(desired.ParentID) || desired.TargetSerial == "" || desired.PhysicalGroup != nil || len(desired.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	binding, err := templateReceiptBinding(desired)
	if err != nil {
		return false, err
	}
	if state == nil {
		return false, nil
	}
	if state.Operation != nil {
		return false, errConfigurationBusy
	}
	owners := 0
	for _, owner := range state.Owners {
		if owner.Section == "inventory" {
			if owner.ConfigName != desired.ConfigName || owner.ConfigUID != desired.ConfigUID {
				return false, errConfigurationOwned
			}
			owners++
		}
	}
	if owners > 1 {
		return false, errConfigurationOwned
	}
	found, applied, ambiguous := false, false, false
	var latest *cvk.NetworkControllerConfigurationActionApplication
	var latestBinding *templateReceiptVersionBinding
	seenResources, seenOperations := map[string]bool{}, map[string]bool{}
	for _, receipt := range state.ActionApplications {
		if receipt.Section != "inventory" || (!strings.HasPrefix(receipt.Resource, "template-deploy:") && !strings.HasPrefix(receipt.Resource, "template-composite-deploy:")) {
			continue
		}
		op := &receipt.Operation
		previous, err := templateReceiptBinding(op)
		if err != nil || op.Resource != receipt.Resource || op.Section != receipt.Section || seenResources[receipt.Resource] || seenOperations[op.ID] {
			return false, errConfigurationStale
		}
		seenResources[receipt.Resource] = true
		seenOperations[op.ID] = true
		if previous.kind != binding.kind || previous.baseID != binding.baseID || op.TargetID != desired.TargetID {
			continue
		}
		if owners != 1 || op.ConfigUID != desired.ConfigUID || op.ConfigName != desired.ConfigName {
			return false, errConfigurationOwned
		}
		if op.Phase != "Verified" || op.Action != "Update" || !configurationOperationID.MatchString(op.ID) || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || op.PhysicalGroup != nil || len(op.CredentialVersions) != 0 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) || receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || receipt.VerifiedAt.Before(op.SubmittedAt) || receipt.VerifiedAt.After(now) || !validAPIID(op.ParentID) || op.TargetSerial == "" {
			return false, errConfigurationStale
		}
		if _, err := canonicalDeploymentID(op.TaskID); err != nil {
			return false, errConfigurationStale
		}
		if op.TargetSerial != desired.TargetSerial {
			return false, errConfigurationStale
		}
		found = true
		if policy == "ON_CHANGE" {
			if latest == nil || receipt.VerifiedAt.After(latest.VerifiedAt.Time) {
				copy := receipt.DeepCopy()
				latest = copy
				latestBinding = previous
				ambiguous = false
			} else if receipt.VerifiedAt.Equal(latest.VerifiedAt) && !equivalentTemplateInputReceipts(&receipt, previous, latest, latestBinding) {
				ambiguous = true
			}
			continue
		}
		sameRevision := op.ConfigGeneration == desired.ConfigGeneration && op.ControllerGeneration == desired.ControllerGeneration && op.SourceUID == desired.SourceUID && op.SourceVersion == desired.SourceVersion
		switch policy {
		case "NEVER":
			applied = true
		case "ALWAYS":
			applied = applied || sameRevision
		}
	}
	if applied {
		return true, nil
	}
	if found && policy == "ON_CHANGE" {
		if ambiguous {
			return false, errConfigurationTransition
		}
		if !reflect.DeepEqual(latestBinding.versions, binding.versions) {
			return false, nil
		}
		if binding.comparison != nil && latestBinding.comparison != nil {
			if binding.comparison.keyID != latestBinding.comparison.keyID {
				return false, errConfigurationSecret
			}
			return binding.comparison.input == latestBinding.comparison.input, nil
		}
		if templateReceiptSameRevision(&latest.Operation, desired) {
			return true, nil
		}
		return false, errConfigurationTransition
	}
	return false, nil
}

func templateReceiptSameRevision(a, b *cvk.NetworkControllerConfigurationOperation) bool {
	return a.ConfigGeneration == b.ConfigGeneration && a.ControllerGeneration == b.ControllerGeneration && a.SourceUID == b.SourceUID && a.SourceVersion == b.SourceVersion
}

func equivalentTemplateInputReceipts(a *cvk.NetworkControllerConfigurationActionApplication, ab *templateReceiptVersionBinding, b *cvk.NetworkControllerConfigurationActionApplication, bb *templateReceiptVersionBinding) bool {
	if !reflect.DeepEqual(ab.versions, bb.versions) {
		return false
	}
	if ab.comparison != nil && bb.comparison != nil {
		return *ab.comparison == *bb.comparison
	}
	return ab.comparison == nil && bb.comparison == nil && templateReceiptSameRevision(&a.Operation, &b.Operation)
}

// Replan from current native state, then consult the existing retained receipts.
// This is a read-only adapter plan; the shared journal supplies submission and
// physical admission through the inventory compiler and shared executor.
func (a *adapter) planOwnedTemplateDeployment(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, device inventoryDeviceIntent, target regularTemplateTarget) (*configurationWrite, error) {
	return a.planOwnedInventoryTemplateDeployment(ctx, config, source, target.redeploy, func(ctx context.Context) (*configurationWrite, error) {
		return a.client.planRegularTemplateDeployment(ctx, device, target)
	})
}
func (a *adapter) planOwnedCompositeTemplateDeployment(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, device inventoryDeviceIntent, target compositeTemplateTarget) (*configurationWrite, error) {
	return a.planOwnedInventoryTemplateDeployment(ctx, config, source, target.redeploy, func(ctx context.Context) (*configurationWrite, error) {
		return a.client.planCompositeTemplateDeployment(ctx, device, target)
	})
}
func (a *adapter) planOwnedInventoryTemplateDeployment(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, policy string, plan func(context.Context) (*configurationWrite, error)) (*configurationWrite, error) {
	if a == nil || config == nil || plan == nil || !validTemplateRedeploy(policy) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 {
		return nil, errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	write, err := plan(ctx)
	if err != nil {
		return nil, err
	}
	if write == nil {
		return nil, errConfigurationTransition
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.Generation != nc.Generation {
		return nil, errConfigurationStale
	}
	desired := &cvk.NetworkControllerConfigurationOperation{ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: fresh.Generation, Section: "inventory", Resource: write.resource, Action: write.action, TargetID: write.identity.target, TargetSerial: write.identity.serial, ParentID: write.identity.parent, DependencyVersions: write.identity.dependencies.DeepCopy()}
	if source != nil {
		desired.SourceUID = string(source.uid)
		desired.SourceVersion = source.version
	}
	applied, err := templateDeploymentAlreadyApplied(fresh.Status.Configuration, desired, policy, time.Now())
	if err != nil {
		return nil, err
	}
	if applied {
		return nil, nil
	}
	return write, nil
}

// Both deployment kinds share receipt policy and ownership validation. Their
// version identities remain explicit, ordered, and contain no secret material.
type templateReceiptVersionBinding struct {
	kind, baseID string
	versions     any
	comparison   *templateInputBinding
	templates    []templateDeploymentVersionBinding
}

func templateReceiptBinding(op *cvk.NetworkControllerConfigurationOperation) (*templateReceiptVersionBinding, error) {
	if op == nil {
		return nil, errConfigurationTransition
	}
	resource, comparison, err := splitTemplateComparisonResource(op.Resource)
	if err != nil {
		return nil, err
	}
	if digest, ok := strings.CutPrefix(resource, "template-composite-deploy:"); ok {
		if len(digest) != 64 || strings.ToLower(digest) != digest {
			return nil, errConfigurationTransition
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, errConfigurationTransition
		}
		graph, err := parseCompositeDeploymentBindings(op.DependencyVersions)
		if err != nil {
			return nil, err
		}
		return &templateReceiptVersionBinding{kind: "composite", baseID: graph.parent.baseID, versions: op.DependencyVersions.Entries, comparison: comparison, templates: append([]templateDeploymentVersionBinding{graph.parent}, graph.members...)}, nil
	}
	if op.DependencyVersions != nil {
		return nil, errConfigurationTransition
	}
	binding, err := parseTemplateDeploymentResource(op.Resource)
	if err != nil {
		return nil, err
	}
	return &templateReceiptVersionBinding{kind: "regular", baseID: binding.baseID, versions: binding.version, comparison: comparison, templates: []templateDeploymentVersionBinding{*binding}}, nil
}

// Both generic public verification and resource-specific completion publish
// through the existing atomic action-receipt writer. Enforce ordering there,
// rather than only in an optional template-specific observation callback.
func validatePrivateTemplateReceiptPublication(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation, now time.Time) error {
	if op == nil || op.Section != "inventory" || !isTemplateDeploymentResource(op.Resource) {
		return nil
	}
	binding, err := templateReceiptBinding(op)
	if err != nil {
		return err
	}
	if binding.comparison == nil {
		return nil
	}
	if state == nil {
		return errConfigurationStale
	}
	history := state.DeepCopy()
	history.Operation = nil
	if _, err := templateDeploymentAlreadyApplied(history, op, "NEVER", now); err != nil {
		return err
	}
	for _, old := range history.ActionApplications {
		if old.Section != "inventory" || !isTemplateDeploymentResource(old.Resource) {
			continue
		}
		previous, err := templateReceiptBinding(&old.Operation)
		if err != nil {
			return err
		}
		// Idempotent publication preserves its own original verification timestamp.
		if old.Operation.ID == op.ID {
			continue
		}
		if previous.kind == binding.kind && previous.baseID == binding.baseID && old.Operation.TargetID == op.TargetID && old.VerifiedAt != nil && !old.VerifiedAt.Time.Before(now.UTC().Truncate(time.Second)) {
			return errConfigurationBusy
		}
	}
	return nil
}
