// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const ownedDiscoveryPrefix = "discovery-owned:"
const ownedDiscoveryReceiptPrefix = "discovery-record:"

func discoveryPublicDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func ownedDiscoveryResource(name string, payload map[string]any) string {
	mask, err := discoveryFieldMask(payload)
	if err != nil {
		return ""
	}
	canonical, err := discoveryCanonicalEffect(payload)
	if err != nil {
		return ""
	}
	return ownedDiscoveryPrefix + discoveryPublicDigest(name) + ":" + strconv.FormatUint(mask, 16) + ":" + discoveryPublicDigest(canonical)
}
func ownedDiscoveryReceiptKey(name, id string) string {
	return ownedDiscoveryReceiptPrefix + discoveryPublicDigest(name) + ":" + id
}
func ownedDiscoveryOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil || !ownedDiscoverySelected(op.Resource) || (op.Section != "inventory" && op.Section != "templates") || op.Section == "templates" && !strings.HasPrefix(op.Resource, "discoveries/"+ownedDiscoveryPrefix) {
		return false
	}
	if discoveryFinalizeSelected(op.Resource) && (op.Section != "inventory" || op.Action != "Delete" || !strings.HasSuffix(op.Resource, discoveryPruneSuffix)) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(discoveryLogicalResource(op.Resource), ownedDiscoveryPrefix), ":")
	mask, hash, valid := ownedDiscoveryFingerprintParts(op.Resource)
	if !valid {
		return false
	}
	for _, part := range []string{parts[0], hash} {
		if len(part) != 64 {
			return false
		}
		if _, err := hex.DecodeString(part); err != nil || strings.ToLower(part) != part {
			return false
		}
	}
	action := (op.Action == "Create" && op.TargetID == "") || (op.Action == "Delete" && validAPIID(op.TargetID) && mask != 0)
	if strings.HasSuffix(op.Resource, discoveryPruneSuffix) && op.Action != "Delete" {
		return false
	}
	return action && op.ParentID == "" && op.TargetSerial == "" && op.CreatedResourceID == "" && op.PhysicalGroup == nil && op.CancellationRequestedAt == nil && op.DependencyVersions == nil && len(op.CredentialVersions) == 0 && len(op.SSIDCredentialHistory) == 0
}

type ownedDiscoveryExecutor struct {
	retired             map[string]bool
	retiredInvalid      bool
	base                *discoveryExecutor
	owner               string
	receipts            []cvk.NetworkControllerConfigurationActionApplication
	prune               bool
	retainedObservation bool
	submissionGuard     func(context.Context, string, string, configurationResourceIdentity) error
}

func (e *ownedDiscoveryExecutor) count() int { return e.base.count() }
func (e *ownedDiscoveryExecutor) receipt(name string) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	return e.receiptByHash(discoveryPublicDigest(name))
}
func validOwnedDiscoveryReceipt(r cvk.NetworkControllerConfigurationActionApplication) bool {
	if r.Section != r.Operation.Section || !ownedDiscoveryOperation(&r.Operation) || r.Operation.Phase != "Verified" || !configurationOperationID.MatchString(r.Operation.ID) || r.Operation.ConfigUID == "" || r.Operation.ConfigName == "" || r.Operation.ConfigGeneration < 1 || r.Operation.ControllerGeneration < 1 || !validAPIID(r.Operation.TaskID) || r.Operation.SubmittedAt == nil || r.Operation.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(r.Operation.SubmittedAt) || r.VerifiedAt.After(time.Now()) {
		return false
	}
	parts := strings.SplitN(strings.TrimPrefix(discoveryLogicalResource(r.Operation.Resource), ownedDiscoveryPrefix), ":", 2)
	prefix := ownedDiscoveryReceiptPrefix + parts[0] + ":"
	id := strings.TrimPrefix(r.Resource, prefix)
	return strings.HasPrefix(r.Resource, prefix) && validAPIID(id) && (r.Operation.Action != "Delete" || id == r.Operation.TargetID)
}

// Select one ordered action only after all declared jobs pass preflight.
func (e *ownedDiscoveryExecutor) selectAction(actual map[string]discoveryMetadata, resolved []map[string]any) (int, bool, error) {
	if err := e.retiredAbsent(actual); err != nil {
		return -1, false, err
	}
	if len(resolved) != len(e.base.desired) {
		return -1, false, errConfigurationTransition
	}
	selected := -1
	replace := false
	pending := false
	for i, want := range e.base.desired {
		// A credential-bearing creator cannot become an imported global-only
		// job by removing its projected leaves. Retain its distinct receipt
		// until an explicit credential-removal/migration flow is qualified.
		for _, receipt := range e.receipts {
			if !discoveryCredentialReceiptSelected(receipt.Resource) {
				continue
			}
			identity, err := parseDiscoveryCredentialReceipt(receipt.Resource)
			opName, valid := discoveryCredentialOperationInput(&receipt.Operation)
			if err != nil || !valid || opName != identity.name || identity.name == discoveryPublicDigest(want.name) {
				return -1, false, errConfigurationOwned
			}
		}
		if want.fields["name"] != want.name || resolved[i]["name"] != want.name {
			return -1, false, errConfigurationTransition
		}
		receipt, err := e.receipt(want.name)
		if err != nil {
			return -1, false, err
		}
		got, present := actual[want.name]
		if receipt != nil {
			prefix := ownedDiscoveryReceiptKey(want.name, "")
			id := strings.TrimPrefix(receipt.Resource, prefix)
			for name, row := range actual {
				if row.ID == id && name != want.name {
					return -1, false, errConfigurationOwned
				}
			}
			if present && (receipt.Resource != ownedDiscoveryReceiptKey(want.name, got.ID) || receipt.Operation.Action == "Delete") {
				return -1, false, errConfigurationOwned
			}
		}
		if !present {
			if receipt != nil && receipt.Operation.Action == "Create" {
				return -1, false, errConfigurationOwned
			}
			if selected < 0 {
				selected = i
			}
			continue
		}
		complete := got.Condition == "Complete" && got.Status == "Inactive"
		matches, err := discoveryMatches(resolved[i], got)
		if err != nil {
			return -1, false, err
		}
		if receipt == nil {
			if !matches {
				return -1, false, errConfigurationOwned
			}
			pending = pending || !complete
			continue
		}
		mask, hash, valid := ownedDiscoveryFingerprintParts(receipt.Operation.Resource)
		if !valid {
			return -1, false, errConfigurationOwned
		}
		if mask == 0 {
			if !matches {
				return -1, false, errConfigurationOwned
			}
			pending = pending || !complete
			continue
		}
		original, err := discoveryNativeEffect(got, mask)
		if err != nil || discoveryPublicDigest(original) != hash {
			return -1, false, errConfigurationOwned
		}
		desired := ownedDiscoveryResource(want.name, resolved[i])
		if desired == "" {
			return -1, false, errConfigurationTransition
		}
		if desired != receipt.Operation.Resource {
			// Delete-by-ID stops and removes an owned job, including an active
			// one. Unchanged desired jobs still wait for execution completion.
			if selected < 0 {
				selected = i
				replace = true
			}
		} else if !complete {
			pending = true
		}
	}
	if pending && selected < 0 {
		return -1, false, errConfigurationBusy
	}
	return selected, replace, nil
}
func (e *ownedDiscoveryExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, resolved, err := e.base.observe(ctx)
	if err != nil {
		return nil, err
	}
	selected, replace, selectionErr := e.selectAction(actual, resolved)
	if selectionErr != nil && !errors.Is(selectionErr, errConfigurationBusy) {
		return nil, selectionErr
	}
	candidates, err := e.pruneCandidates(actual)
	if err != nil {
		return nil, err
	}
	if len(candidates) > 0 {
		return e.prunePlan(candidates, resolved)
	}
	if selectionErr != nil {
		return nil, selectionErr
	}
	if selected < 0 {
		return nil, nil
	}
	if replace {
		return e.deletePlan(selected, resolved)
	}
	write, err := e.base.createPlan(selected, resolved, func(actual map[string]discoveryMetadata, resolved []map[string]any) (int, error) {
		index, replace, err := e.selectAction(actual, resolved)
		if replace {
			return -1, errConfigurationStale
		}
		return index, err
	}, func(ctx context.Context) error {
		resource := ownedDiscoveryResource(e.base.desired[selected].name, resolved[selected])
		return e.guard(ctx, resource, "Create", configurationResourceIdentity{})
	})
	if err != nil {
		return nil, err
	}
	write.resource = ownedDiscoveryResource(e.base.desired[selected].name, resolved[selected])
	if write.resource == "" {
		return nil, errConfigurationTransition
	}
	return write, nil
}
func (e *ownedDiscoveryExecutor) deletePlan(selected int, resolved []map[string]any) (*configurationWrite, error) {
	want := e.base.desired[selected]
	receipt, err := e.receipt(want.name)
	if err != nil || receipt == nil {
		return nil, errConfigurationOwned
	}
	resource := receipt.Operation.Resource
	target := strings.TrimPrefix(receipt.Resource, ownedDiscoveryReceiptKey(want.name, ""))
	snapshot, _ := json.Marshal(resolved)
	var used atomic.Bool
	return &configurationWrite{resource: resource, action: "Delete", identity: configurationResourceIdentity{target: target}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		actual, fresh, err := e.base.observe(ctx)
		if err != nil {
			return Task{}, err
		}
		raw, _ := json.Marshal(fresh)
		if !bytes.Equal(snapshot, raw) {
			return Task{}, errConfigurationStale
		}
		index, replace, err := e.selectAction(actual, fresh)
		if err != nil {
			return Task{}, err
		}
		if index != selected || !replace || actual[want.name].ID != target {
			return Task{}, errConfigurationStale
		}
		if err := e.guard(ctx, resource, "Delete", configurationResourceIdentity{target: target}); err != nil {
			return Task{}, err
		}
		return e.base.client.deleteDiscovery(ctx, target)
	}}, nil
}
func (e *ownedDiscoveryExecutor) verifyOwned(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, bool, error) {
	if !ownedDiscoveryOperation(op) || !validAPIID(op.TaskID) {
		return "", false, errConfigurationTransition
	}
	op = localDiscoveryOperation(op)
	if op.Action == "Delete" {
		hash := discoveryOperationNameHash(op.Resource)
		receipt, err := e.receiptByHash(hash)
		if err != nil || receipt == nil || receipt.Operation.Action != "Create" || receipt.Resource != ownedDiscoveryReceiptPrefix+hash+":"+op.TargetID || receipt.Operation.Resource != strings.TrimSuffix(op.Resource, discoveryPruneSuffix) {
			return "", false, errConfigurationOwned
		}
		for range 2 {
			actual, err := e.base.client.discoveries(ctx)
			if err != nil {
				return "", false, err
			}
			if err := e.retiredAbsent(actual); err != nil {
				return "", false, err
			}
			for name, row := range actual {
				if row.ID == op.TargetID {
					return "", false, nil
				}
				if discoveryPublicDigest(name) == hash {
					return "", false, errConfigurationOwned
				}
			}
		}
		return op.TargetID, true, nil
	}
	id, err := e.base.client.discoveryTaskResource(ctx, op.TaskID)
	if err != nil {
		return "", false, err
	}
	actual, err := e.base.client.discoveries(ctx)
	if err != nil {
		return "", false, err
	}
	if err := e.retiredAbsent(actual); err != nil {
		return "", false, err
	}
	if e.retainedObservation {
		mask, _, valid := ownedDiscoveryFingerprintParts(op.Resource)
		if !valid || mask == 0 {
			return "", false, errConfigurationOwned
		}
		for name, row := range actual {
			if discoveryPublicDigest(name) != discoveryOperationNameHash(op.Resource) {
				continue
			}
			if row.ID != id {
				return "", false, errConfigurationOwned
			}
			if err := verifyOwnedDiscoveryEffect(row, op.Resource); err != nil {
				return "", false, err
			}
			return id, true, nil
		}
		return "", false, nil
	}
	for _, want := range e.base.desired {
		if !strings.HasPrefix(op.Resource, ownedDiscoveryPrefix+discoveryPublicDigest(want.name)+":") {
			continue
		}
		got, present := actual[want.name]
		if !present {
			return "", false, nil
		}
		if got.ID != id {
			return "", false, errConfigurationOwned
		}
		mask, _, valid := ownedDiscoveryFingerprintParts(op.Resource)
		if !valid {
			return "", false, errConfigurationTransition
		}
		if mask != 0 {
			if err := verifyOwnedDiscoveryEffect(got, op.Resource); err != nil {
				return "", false, err
			}
			// The receipt proves native creation, not completion of discovery.
			// next/selectAction retains the execution gate before InSync.
			return id, true, nil
		}
		// Legacy result records did not retain a field mask. Recover against the
		// unchanged source only; they do not authorize future replacement by shape.
		effect := map[string]any{}
		for key, value := range want.fields {
			effect[key] = value
		}
		ids := slices.Clone(got.CredentialIDs)
		slices.Sort(ids)
		effect["globalCredentialIdList"] = ids
		if ownedDiscoveryPrefix+discoveryPublicDigest(want.name)+":"+discoveryPublicDigest(effect) != op.Resource {
			return "", false, errConfigurationStale
		}
		matches, err := discoveryMatches(effect, got)
		return id, matches && got.Condition == "Complete" && got.Status == "Inactive", err
	}
	return "", false, errConfigurationTransition
}
func (e *ownedDiscoveryExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || (op.Section != "inventory" && op.Section != "templates") {
		return false, errConfigurationTransition
	}
	_, done, err := e.verifyOwned(ctx, localDiscoveryOperation(op))
	return done, err
}

func (a *adapter) compileOwnedDiscoveryApply(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (configurationApplyPlan, bool, error) {
	var plan configurationApplyPlan
	if len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || source == nil || len(config.Spec.SecretRefs) != 0 {
		return plan, false, nil
	}
	// Retained credential ownership, including an empty desired source, stays
	// with its typed lifecycle. The ordinary route cannot drop that audit.
	if discoveryCredentialHistoryOwner(nc, config) || discoveryNoAuthInitialSource(nc, config, source) {
		return plan, false, nil
	}
	if tagPruneSource(source.raw, config.Spec.ManagedSections) && (tagOwnershipHistory(nc, config) || !pureDiscoverySource(source.raw)) {
		return plan, false, nil
	}
	pure := pureDiscoverySource(source.raw)
	if config.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && (!pure || !discoveryConfigSpecShape(config) || !hasDiscoveryConfigFinalizer(config)) {
		return plan, true, errConfigurationTransition
	}
	prune := config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete && discoveryPruneSelected(nc, config, source)
	if prune {
		if err := validateDiscoveryPruneScope(nc, config, source); err != nil {
			return plan, true, err
		}
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil && !strings.HasPrefix(nc.Status.Configuration.Operation.Resource, ownedDiscoveryPrefix) {
		if prune {
			return plan, true, errConfigurationBusy
		}
		return plan, false, nil
	}
	intent, err := parseInventoryIntent(source.raw)
	if err != nil {
		return plan, prune, err
	}
	if len(intent.tags) != 0 || len(intent.devices) != 0 || len(intent.discoveries) == 0 && !pure {
		return plan, false, nil
	}
	e := &ownedDiscoveryExecutor{base: &discoveryExecutor{client: a.client, desired: intent.discoveries}, owner: string(config.UID), prune: prune}
	if nc.Status.Configuration != nil {
		e.receipts = nc.Status.Configuration.DeepCopy().ActionApplications
	}
	plan = configurationApplyPlan{executor: e, section: "inventory", sections: []configapi.NetworkControllerSectionStatus{{Name: "inventory", ResourceCount: int64(e.count())}}}
	a.bindOwnedDiscoveryExecutor(nc, config, source, &plan, e, "")
	return plan, true, nil
}
func ownedDiscoveryCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation, history ...map[string]bool) error {
	if !ownedDiscoveryOperation(op) {
		return errConfigurationTransition
	}
	prefix := ownedDiscoveryReceiptPrefix + discoveryOperationNameHash(op.Resource) + ":"
	receipts := make([]cvk.NetworkControllerConfigurationActionApplication, 0, len(state.ActionApplications)+1)
	found := false
	for _, r := range state.ActionApplications {
		if len(history) == 1 && history[0][r.Resource] && r.Operation.ConfigUID != op.ConfigUID {
			if !validOwnedDiscoveryReceipt(r) || r.Operation.Action != "Delete" || !strings.HasSuffix(r.Operation.Resource, discoveryPruneSuffix) {
				return errConfigurationOwned
			}
			receipts = append(receipts, r)
			continue
		}
		if strings.HasPrefix(r.Resource, prefix) {
			if found || r.Operation.ConfigUID != op.ConfigUID || !validOwnedDiscoveryReceipt(r) {
				return errConfigurationOwned
			}
			if op.Action == "Delete" && (r.Operation.Action != "Create" || discoveryLogicalResource(r.Operation.Resource) != strings.TrimSuffix(discoveryLogicalResource(op.Resource), discoveryPruneSuffix) || strings.TrimPrefix(r.Resource, prefix) != op.TargetID) {
				return errConfigurationOwned
			}
			if op.Action == "Create" && r.Operation.Action == "Delete" && strings.HasSuffix(r.Operation.Resource, discoveryPruneSuffix) && !configurationInputAfter(op, &r.Operation) {
				return errConfigurationStale
			}
			found = true
		} else {
			receipts = append(receipts, r)
		}
	}
	if op.Action == "Delete" && !found {
		return errConfigurationOwned
	}
	prospective := *op.DeepCopy()
	prospective.Phase = "Verified"
	prospective.TaskID = strings.Repeat("x", 128)
	now := metav1.Now()
	prospective.SubmittedAt = &now
	receipts = append(receipts, cvk.NetworkControllerConfigurationActionApplication{Section: op.Section, Resource: prefix + strings.Repeat("x", 128), Operation: prospective, VerifiedAt: &now})
	raw, err := json.Marshal(receipts)
	if err != nil || len(receipts) > 256 || len(raw) > 256<<10 {
		return errConfigurationTransition
	}
	return nil
}
func ownedDiscoveryReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation, history ...map[string]bool) bool {
	if !ownedDiscoveryOperation(op) || op.Phase != "Verified" {
		return false
	}
	count := 0
	logical := 0
	prefix := ownedDiscoveryReceiptPrefix + discoveryOperationNameHash(op.Resource) + ":"
	for _, r := range state.ActionApplications {
		if len(history) == 1 && history[0][r.Resource] && r.Operation.ConfigUID != op.ConfigUID {
			if !validOwnedDiscoveryReceipt(r) || r.Operation.Action != "Delete" || !strings.HasSuffix(r.Operation.Resource, discoveryPruneSuffix) {
				return false
			}
			continue
		}
		if strings.HasPrefix(r.Resource, prefix) {
			logical++
		}
		if r.Operation.ID == op.ID {
			if !validOwnedDiscoveryReceipt(r) || !reflect.DeepEqual(r.Operation, *op) {
				return false
			}
			count++
		}
	}
	return count == 1 && logical == 1
}
