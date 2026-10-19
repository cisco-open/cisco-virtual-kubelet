// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Resource semantics only. Submission permits, phases, task timeouts and slot
// release belong to the existing configuration loop, not this executor.
type discoveryCredentialExecutor struct {
	a          *adapter
	config     *configapi.NetworkControllerConfig
	source     *configSourceSnapshot
	projection *discoveryCredentialProjection
	// Global-only rows retain the existing owned discovery implementation and
	// unprefixed journal identities. No second ownership state machine is used.
	ordinary *ownedDiscoveryExecutor
}

// Resource-specific lifecycle admission; complete source and secret authorization remain
// the compiler's responsibility. Do not widen generic inventory secret support.
// Metadata-only enrollment shape. Actual source purity and projection values
// remain worker checks; unenrolled Delete-policy intent cannot mutate.
func discoveryCredentialDeletionSpecShape(c *configapi.NetworkControllerConfig) bool {
	if c == nil || c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyDelete || len(c.Spec.SecretRefs) == 0 {
		return false
	}
	copy := c.DeepCopy()
	copy.Spec.DeletionPolicy = configapi.NetworkControllerRetentionPolicyRetain
	return discoveryCredentialApplyShape(copy) && supportedConfigurationApply(copy)
}

// A delivered owner may remove its last projected leaf. This is metadata
// admission only: the complete cohort, custody seal and current source must
// still validate. Initial NoAuth metadata uses its separate source-gated route;
// this predicate alone grants neither enrollment nor cleanup authority.
func discoveryCredentialOwnedDeletionSpecShape(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	return discoveryCredentialDeletionSpecShape(c) || c != nil && c.UID != "" &&
		c.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete &&
		len(c.Spec.SecretRefs) == 0 && hasDiscoveryConfigFinalizer(c) &&
		!hasFeatureConfigFinalizer(c) && discoveryCredentialLifecycleShape(c) &&
		discoveryCredentialHistoryOwner(nc, c)
}

func discoveryCredentialApplyShape(c *configapi.NetworkControllerConfig) bool {
	return discoveryCredentialLifecycleShape(c) && len(c.Spec.SecretRefs) > 0
}
func discoveryCredentialLifecycleShape(c *configapi.NetworkControllerConfig) bool {
	if c == nil || c.Spec.Mode != configapi.NetworkControllerApplyModeApply || len(c.Spec.ManagedSections) != 1 || c.Spec.ManagedSections[0] != "inventory" ||
		c.Spec.PrunePolicy != "" && c.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyRetain && c.Spec.PrunePolicy != configapi.NetworkControllerRetentionPolicyDelete || c.Spec.DeletionPolicy != "" && c.Spec.DeletionPolicy != configapi.NetworkControllerRetentionPolicyRetain && !(c.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && hasDiscoveryConfigFinalizer(c) && !hasFeatureConfigFinalizer(c)) {
		return false
	}
	for _, ref := range c.Spec.SecretRefs {
		parts := strings.Split(ref.Path, "/")
		if ref.Section != "inventory" || ref.Use != "" && ref.Use != "Value" || len(parts) != 4 || parts[0] != "" || parts[1] != "discovery" {
			return false
		}
		index, err := strconv.Atoi(parts[2])
		if err != nil || index < 0 || index >= 100 || strconv.Itoa(index) != parts[2] {
			return false
		}
		known := false
		for _, field := range discoveryCredentialFields {
			known = known || field.model == parts[3]
		}
		if !known {
			return false
		}
	}
	return true
}

func (e *discoveryCredentialExecutor) count() int { return len(e.projection.intents) }

func discoveryCredentialHistoryValid(state *cvk.NetworkControllerConfigurationStatus, owner string) bool {
	_, err := discoveryCredentialStructuralLineage(state, owner)
	return err == nil
}

func (e *discoveryCredentialExecutor) observe(ctx context.Context) (*cvk.NetworkController, map[string]discoveryMetadata, []*discoveryCredentialRequest, []string, error) {
	if err := e.a.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
		return nil, nil, nil, nil, err
	}
	nc, err := e.a.currentController(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	history, err := discoveryCredentialExecutionHistory(nc, e.config, e.source)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// Bind every job before selecting a write; unknown/unsupported rows cannot
	// be ignored just because an earlier job is ready for creation.
	var globals map[string]map[string]globalCredentialMetadata
	for _, want := range e.projection.intents {
		if len(want.base.credentials) > 0 {
			globals, err = e.a.client.globalCredentials(ctx)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			break
		}
	}
	requests := make([]*discoveryCredentialRequest, e.count())
	inputs := make([]string, e.count())
	for i := range requests {
		requests[i], inputs[i], err = e.projection.bindRequest(nc, e.config, e.source, i, globals)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if e.projection.tracks(e.projection.intents[i]) {
			inputs[i], err = history.input(discoveryPublicDigest(e.projection.intents[i].base.name), inputs[i])
			if err != nil {
				return nil, nil, nil, nil, err
			}
		}
	}
	if discoveryCredentialCustodyRequired(nc, string(e.config.UID)) {
		record, _, err := discoveryCredentialCurrentCustody(nc, e.config, e.source)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		initial, err := discoveryCredentialRevisionContext(record, e.config, string(e.source.uid), e.source.version, featureIdentityDigest(e.source.raw))
		if err != nil {
			return nil, nil, nil, nil, err
		}
		if initial && featureIdentityDigest(inputs) != record.CredentialCustody.InputsDigest {
			return nil, nil, nil, nil, errConfigurationStale
		}
	}
	actual, err := e.a.client.discoveries(ctx)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	if err := history.absentRetired(actual); err != nil {
		return nil, nil, nil, nil, err
	}
	return nc, actual, requests, inputs, nil
}

func (e *discoveryCredentialExecutor) selectCreate(nc *cvk.NetworkController, actual map[string]discoveryMetadata, requests []*discoveryCredentialRequest, inputs []string) (int, error) {
	if len(requests) != e.count() || len(inputs) != e.count() {
		return -1, errConfigurationTransition
	}
	ordinaryPending := false
	if e.ordinary != nil {
		resolved := []map[string]any{}
		for i, want := range e.projection.intents {
			if !e.projection.tracks(want) {
				resolved = append(resolved, requests[i].public)
			}
		}
		if _, _, err := e.ordinary.selectAction(actual, resolved); err != nil {
			if !errors.Is(err, errConfigurationBusy) {
				return -1, err
			}
			ordinaryPending = true
		}
	}
	history, err := discoveryCredentialExecutionHistory(nc, e.config, e.source)
	if err != nil {
		return -1, err
	}
	selected, pending := -1, ordinaryPending
	for i, want := range e.projection.intents {
		if !e.projection.tracks(want) {
			continue
		}
		name := discoveryPublicDigest(want.base.name)
		if nc.Status.Configuration != nil {
			for _, entry := range nc.Status.Configuration.ActionApplications {
				if strings.HasPrefix(entry.Resource, ownedDiscoveryReceiptPrefix+name+":") {
					return -1, errConfigurationOwned
				}
			}
		}
		creator := history.latest[name]
		got, present := actual[want.base.name]
		if creator == nil || history.retired[creator.receipt.Operation.ID] != nil {
			if present {
				return -1, errConfigurationOwned
			}
			if err := history.transition(name, requests[i].versions); err != nil {
				return -1, err
			}
			if selected < 0 {
				selected = i
			}
			continue
		}
		if creator.receipt.Operation.ConfigName != e.config.Name || !present || creator.identity.id != got.ID {
			return -1, errConfigurationOwned
		}
		if !creator.identity.legacy {
			actualEffect, err := nativeDiscoveryCredentialEffect(got, creator.identity.effect.mask)
			if err != nil || actualEffect != creator.identity.effect {
				return -1, errConfigurationOwned
			}
		}
		if creator.receipt.Operation.Resource != inputs[i] {
			// Legacy receipts must first acquire a public-field proof under their
			// unchanged input. A changed source cannot manufacture that old proof.
			if creator.identity.legacy {
				return -1, errConfigurationOwned
			}
			if err := history.transition(name, requests[i].versions); err != nil {
				return -1, err
			}
			if selected < 0 {
				selected = i
			}
			continue
		}
		matches, err := discoveryMatches(requests[i].public, got)
		if err != nil {
			return -1, err
		}
		if !matches {
			return -1, errConfigurationOwned
		}
		if !creator.identity.legacy {
			wantEffect, err := discoveryCredentialEffect(requests[i].public)
			if err != nil || wantEffect != creator.identity.effect {
				return -1, errConfigurationOwned
			}
		}
		pending = pending || got.Condition != "Complete" || got.Status != "Inactive"
	}
	if pending && selected < 0 {
		return -1, errConfigurationBusy
	}
	return selected, nil
}

func (e *discoveryCredentialExecutor) next(ctx context.Context) (*configurationWrite, error) {
	nc, actual, requests, inputs, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	selected, selectionErr := e.selectCreate(nc, actual, requests, inputs)
	if selectionErr != nil && !errors.Is(selectionErr, errConfigurationBusy) {
		return nil, selectionErr
	}
	candidate, err := e.selectPrune(nc, actual)
	if err != nil {
		return nil, err
	}
	if candidate != nil {
		return e.retirePlan(-1, nc, candidate, requests, inputs)
	}
	if selectionErr == nil {
		if promoted, err := e.promoteLegacyEffect(ctx, nc, actual, requests, inputs); promoted {
			if err != nil {
				return nil, err
			}
			return nil, errConfigurationReplan
		}
	}
	if e.ordinary != nil {
		write, err := e.ordinary.next(ctx)
		if err != nil || write != nil {
			return write, err
		}
	}
	if selectionErr != nil {
		return nil, selectionErr
	}
	if selected < 0 {
		return nil, nil
	}
	history, err := discoveryCredentialExecutionHistory(nc, e.config, e.source)
	if err != nil {
		return nil, err
	}
	creator := history.latest[discoveryPublicDigest(e.projection.intents[selected].base.name)]
	if creator != nil && history.retired[creator.receipt.Operation.ID] == nil {
		return e.retirePlan(selected, nc, creator, requests, inputs)
	}
	body, err := json.Marshal(requests[selected].fields)
	if err != nil {
		return nil, errConfigurationSecret
	}
	identity := configurationResourceIdentity{credentials: slices.Clone(requests[selected].versions)}
	resource := inputs[selected]
	write := &configurationWrite{resource: resource, action: "Create", identity: identity, submit: func(ctx context.Context) (Task, error) {
		return e.a.client.createDiscovery(ctx, body)
	}}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		fresh, actual, currentRequests, currentInputs, err := e.observe(ctx)
		if err != nil {
			return err
		}
		if !slices.Equal(inputs, currentInputs) {
			return errConfigurationStale
		}
		index, err := e.selectCreate(fresh, actual, currentRequests, currentInputs)
		if err != nil {
			return err
		}
		if index != selected {
			return errConfigurationStale
		}
		currentBody, err := json.Marshal(currentRequests[selected].fields)
		if err != nil || !bytes.Equal(body, currentBody) {
			return errConfigurationSecret
		}
		valid := func(op *cvk.NetworkControllerConfigurationOperation) bool {
			_, ok := discoveryCredentialOperationInput(op)
			return ok
		}
		err = e.a.checkOwnedConfigurationDispatch(ctx, fresh, e.config, e.source, "inventory", resource, "Create", identity, valid, func(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
			if !slices.Equal(op.CredentialVersions, identity.credentials) || !discoveryCredentialHistoryValid(state, string(e.config.UID)) {
				return errConfigurationOwned
			}
			if fresh.Status.Configuration == nil || !reflect.DeepEqual(fresh.Status.Configuration.ActionApplications, state.ActionApplications) || !reflect.DeepEqual(fresh.Status.Configuration.CredentialApplications, state.CredentialApplications) {
				return errConfigurationStale
			}
			return discoveryCredentialCreateCapacity(state, op)
		})
		if err != nil {
			return err
		}
		return nil
	})
	return write, nil
}

func (e *discoveryCredentialExecutor) operationIndex(op *cvk.NetworkControllerConfigurationOperation) (int, error) {
	if op != nil && discoveryFinalizeSelected(op.Resource) {
		return -1, errConfigurationTransition
	}
	name, valid := discoveryCredentialOperationInput(op)
	if op != nil && discoveryCredentialRetirementSelected(op.Resource) {
		data, ok := discoveryCredentialRetirementParts(op)
		if ok && data.prune {
			valid = e.config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete && data.scope == featureIdentityDigest(e.source.raw)
		} else if ok {
			name, valid = discoveryCredentialOperationInput(&cvk.NetworkControllerConfigurationOperation{Section: "inventory", Action: "Create", Resource: data.next, CredentialVersions: op.CredentialVersions})
		} else {
			valid = false
		}
	}
	if !valid || op.ConfigUID != string(e.config.UID) || op.ConfigName != e.config.Name || op.ConfigGeneration != e.config.Generation || op.ControllerGeneration != e.a.generation || op.SourceUID != string(e.source.uid) || op.SourceVersion != e.source.version {
		return -1, errConfigurationStale
	}
	if data, ok := discoveryCredentialRetirementParts(op); ok && data.prune {
		return -1, nil
	}
	for i, want := range e.projection.intents {
		if e.projection.tracks(want) && discoveryPublicDigest(want.base.name) == name {
			return i, nil
		}
	}
	return -1, errConfigurationStale
}

func (e *discoveryCredentialExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if e.ordinary != nil && op != nil && ownedDiscoverySelected(op.Resource) {
		return e.ordinary.verify(ctx, op)
	}
	index, err := e.operationIndex(op)
	if err != nil {
		return false, err
	}
	if discoveryCredentialRetirementSelected(op.Resource) {
		_, err = e.observeRetirement(ctx, index, op)
		return err == nil, err
	}
	_, err = e.a.observeDiscoveryCredentialCreate(ctx, e.config, e.source, e.projection, index, op)
	return err == nil, err
}

// The bounded public compiler shares the existing dispatcher and journal loop.
// Owned replacement uses the same journal; pruning and source deletion remain
// separate lifecycle capabilities.
func (a *adapter) compileDiscoveryCredentialApply(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, error) {
	var plan configurationApplyPlan
	if !discoveryCredentialLifecycleShape(c) || len(c.Spec.SecretRefs) == 0 && !discoveryCredentialHistoryOwner(nc, c) && !discoveryNoAuthInitialSource(nc, c, source) {
		return plan, errConfigurationTransition
	}
	p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
	if err != nil {
		return plan, err
	}
	if c.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete {
		if discoveryOrdinaryHistoryOwner(nc, c) {
			return plan, errConfigurationOwned
		}
		for _, intent := range p.intents {
			if len(intent.credentials) == 0 && !p.metadataLifecycle {
				return plan, errConfigurationTransition
			}
		}
	}
	if len(p.intents) == 0 && !discoveryCredentialHistoryOwner(nc, c) {
		return plan, errConfigurationTransition
	}
	globalOnly := []discoveryIntent{}
	for _, want := range p.intents {
		if len(want.credentials) == 0 && !p.metadataLifecycle {
			if len(want.metadata) > 0 || len(want.base.credentials) == 0 {
				return plan, errConfigurationSecret
			}
			globalOnly = append(globalOnly, want.base)
		}
	}
	e := &discoveryCredentialExecutor{a: a, config: c, source: source, projection: p}
	plan = configurationApplyPlan{section: "inventory", executor: e, sections: []configapi.NetworkControllerSectionStatus{{Name: "inventory", ResourceCount: int64(e.count())}}}
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		_, err := e.operationIndex(op)
		return err == nil
	}
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		_, err := e.operationIndex(op)
		return err == nil && op.Phase == "Unknown" && validAPIID(op.TaskID) && op.SubmittedAt != nil && !op.SubmittedAt.IsZero() && !op.SubmittedAt.After(time.Now())
	}
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if _, err := e.operationIndex(op); err != nil {
			return false, err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return false, err
		}
		if discoveryCredentialCustodyRequired(current, string(c.UID)) {
			if _, err := discoveryCredentialExecutionHistory(current, c, source); err != nil {
				return false, err
			}
		}
		if op.SubmittedAt == nil || op.SubmittedAt.IsZero() {
			return false, errConfigurationTransition
		}
		if discoveryCredentialRetirementSelected(op.Resource) {
			return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		}
		_, done, err := a.client.completedDiscoveryTaskResource(ctx, op.TaskID, op.SubmittedAt.Time, now)
		return done, err
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		index, err := e.operationIndex(op)
		if err != nil {
			return err
		}
		if discoveryCredentialRetirementSelected(op.Resource) {
			return e.recordRetirement(ctx, index, op)
		}
		return a.recordDiscoveryCredentialCreate(ctx, c, source, p, index, op)
	}
	if len(globalOnly) > 0 || discoveryOrdinaryHistoryOwner(nc, c) {
		ordinary := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: a.client, desired: globalOnly}, owner: string(c.UID), prune: c.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete}
		if nc.Status.Configuration != nil {
			ordinary.receipts = nc.Status.Configuration.DeepCopy().ActionApplications
		}
		e.ordinary = ordinary
		a.bindOwnedDiscoveryExecutor(nc, c, source, &plan, ordinary, "")
		prior := ordinary.submissionGuard
		ordinary.submissionGuard = func(ctx context.Context, resource, action string, identity configurationResourceIdentity) error {
			fresh, actual, requests, inputs, err := e.observe(ctx)
			if err != nil {
				return err
			}
			if _, err := e.selectCreate(fresh, actual, requests, inputs); err != nil && !(errors.Is(err, errConfigurationBusy) && action == "Delete" && strings.HasSuffix(resource, discoveryPruneSuffix)) {
				return err
			}
			if action == "Create" {
				matched := false
				for i, want := range p.intents {
					if len(want.credentials) == 0 && ownedDiscoveryResource(want.base.name, requests[i].public) == resource {
						matched = true
					}
				}
				if !matched {
					return errConfigurationStale
				}
			}
			return prior(ctx, resource, action, identity)
		}
	}
	return plan, nil
}
