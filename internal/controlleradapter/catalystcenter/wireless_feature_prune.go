// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const featurePruneEmpty = `{"catalyst_center":{"feature_templates":{"wireless":{"cleanair":[],"rrm_fra":[]}}}}`

const featureDeletePrefix = "feature-delete:v1:"
const featureDeletedPrefix = "feature-deleted:v1:"

var errFeaturePruneReferenced = errors.New("feature definition is referenced or complete profile reference evidence is unavailable")

func featureDeleteSelected(resource string) bool {
	return strings.HasPrefix(resource, featureDeletePrefix) || featureFinalizeSelected(resource)
}
func featureDeleteKey(op *cvk.NetworkControllerConfigurationOperation) (string, bool) {
	if op == nil || !featureDeleteSelected(op.Resource) || op.Action != "Delete" {
		return "", false
	}
	copy := *op
	copy.Action = "Update"
	copy.Resource = featureUpdatePrefix + strings.TrimPrefix(featureDeleteCanonical(op.Resource), featureDeletePrefix)
	return featureUpdateKey(&copy)
}
func featureLastState(state *cvk.NetworkControllerConfigurationStatus, key string) (cvk.NetworkControllerConfigurationActionApplication, string, string, error) {
	creators, err := featureCreatedRecords(state)
	if err != nil {
		return cvk.NetworkControllerConfigurationActionApplication{}, "", "", err
	}
	applied, err := featureAppliedRecords(state)
	if err != nil {
		return cvk.NetworkControllerConfigurationActionApplication{}, "", "", err
	}
	r, exists := creators[key]
	if !exists {
		return r, "", "", errConfigurationOwned
	}
	prefix := featureCreatedPrefix
	if updated, ok := applied[key]; ok {
		r = updated
		prefix = featureAppliedPrefix
	}
	id, hash, err := featureReceiptIdentity(r.Resource, prefix, key)
	if err != nil || !validWirelessFeatureFingerprint(hash) {
		return r, "", "", errConfigurationOwned
	}
	return r, id, hash, nil
}
func featureDeleteAuthority(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	key, ok := featureDeleteKey(op)
	if !ok {
		return errConfigurationTransition
	}
	r, id, hash, err := featureLastState(state, key)
	if err != nil || op.TargetID != id || strings.TrimPrefix(featureDeleteCanonical(op.Resource), featureDeletePrefix) != key+":"+hash || op.ConfigUID != r.Operation.ConfigUID || op.ConfigName != r.Operation.ConfigName || op.ConfigGeneration < r.Operation.ConfigGeneration || op.ControllerGeneration < r.Operation.ControllerGeneration {
		return errConfigurationOwned
	}
	if op.SubmittedAt != nil && op.SubmittedAt.Before(r.VerifiedAt) {
		return errConfigurationStale
	}
	return nil
}
func featureDeletedRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	records := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return records, nil
	}
	for _, r := range state.ActionApplications {
		if featureRetiredSelected(r.Resource) {
			continue
		}
		if !strings.HasPrefix(r.Resource, featureDeletedPrefix) && !featureDeleteSelected(r.Operation.Resource) {
			continue
		}
		op := &r.Operation
		key, ok := featureDeleteKey(op)
		id, hash, err := featureReceiptIdentity(r.Resource, featureDeletedPrefix, key)
		if !ok || err != nil || hash != "" || id != op.TargetID || r.Section != op.Section || op.Phase != "Verified" || !configurationOperationID.MatchString(op.ID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) || featureDeleteAuthority(state, op) != nil {
			return nil, errConfigurationOwned
		}
		if _, duplicate := records[key]; duplicate {
			return nil, errConfigurationOwned
		}
		records[key] = r
	}
	return records, nil
}
func featureDeleteCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if _, err := featureRetiredCohorts(state); err != nil {
		return err
	}
	if err := featureDeleteAuthority(state, op); err != nil {
		return err
	}
	records, err := featureDeletedRecords(state)
	if err != nil {
		return err
	}
	key, _ := featureDeleteKey(op)
	if _, exists := records[key]; exists {
		return errConfigurationOwned
	}
	if len(state.ActionApplications) >= 256 {
		return errConfigurationBusy
	}
	return nil
}
func featureDeletedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if _, err := featureRetiredCohorts(state); err != nil {
		return false
	}
	key, ok := featureDeleteKey(op)
	if !ok || op.Phase != "Verified" {
		return false
	}
	records, err := featureDeletedRecords(state)
	r, exists := records[key]
	if err != nil || !exists || !reflect.DeepEqual(r.Operation, *op) {
		return false
	}
	count := 0
	for _, row := range state.ActionApplications {
		if row.Operation.ID == op.ID {
			count++
		}
	}
	return count == 1
}

type featurePruneCandidate struct{ Key, Kind, Name, ID, Fingerprint string }
type featurePruneExecutor struct {
	base     *featureOwnedCreateExecutor
	guard    func(context.Context, *configurationWrite) error
	resource func(featurePruneCandidate) string
}

func (e *featurePruneExecutor) count() int { return e.base.count() }
func (e *featurePruneExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op != nil && featureDeleteSelected(op.Resource) {
		return e.verifyDelete(ctx, op)
	}
	return e.base.verify(ctx, op)
}
func featureKind(key string) string {
	if strings.HasPrefix(key, "ca:") {
		return wirelessCleanAirKind
	}
	return wirelessFRAKind
}
func featureNameKey(kind, name string) string {
	if kind == wirelessCleanAirKind {
		return "ca:" + featureIdentityDigest(name)
	}
	return "fra:" + featureIdentityDigest(name)
}
func (e *featurePruneExecutor) snapshot(ctx context.Context) ([]featurePruneCandidate, error) {
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: e.base.receipts}
	creators, err := featureCreatedRecords(state)
	if err != nil {
		return nil, err
	}
	if _, err = featureAppliedRecords(state); err != nil {
		return nil, err
	}
	deleted, err := featureDeletedRecords(state)
	if err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, binding := range e.base.bindings {
		wanted[binding.key()] = true
	}
	inventory := map[string]map[string]wirelessFeatureInstance{}
	candidates := []featurePruneCandidate{}
	for key, creator := range creators {
		if creator.Operation.ConfigUID != e.base.owner {
			continue
		}
		if creator.Operation.ConfigName != e.base.ownerName {
			return nil, errConfigurationOwned
		}
		if wanted[key] {
			continue
		}
		kind := featureKind(key)
		rows, present := inventory[kind]
		if !present {
			rows, err = e.base.client.wirelessFeatureInstances(ctx, kind)
			if err != nil {
				return nil, err
			}
			inventory[kind] = rows
		}
		originalID, err := featureCreatedTarget(creator)
		if err != nil {
			return nil, err
		}
		var selected *wirelessFeatureInstance
		for _, instance := range rows {
			if instance.ID == originalID || featureNameKey(kind, instance.Name) == key {
				if selected != nil || instance.ID != originalID || featureNameKey(kind, instance.Name) != key || *instance.System {
					return nil, errConfigurationOwned
				}
				copy := instance
				selected = &copy
			}
		}
		if _, done := deleted[key]; done {
			if selected != nil {
				return nil, errConfigurationOwned
			}
			continue
		}
		if selected == nil {
			return nil, errConfigurationOwned
		} // disappearance is not a CVK tombstone
		_, id, hash, err := featureLastState(state, key)
		if err != nil || id != selected.ID {
			return nil, errConfigurationOwned
		}
		var observed string
		if kind == wirelessCleanAirKind {
			_, observed, err = e.base.client.wirelessCleanAirReadState(ctx, *selected)
		} else {
			_, observed, err = e.base.client.wirelessFRAReadState(ctx, *selected)
		}
		if err != nil {
			return nil, err
		}
		if observed != hash {
			return nil, errConfigurationOwned
		}
		candidates = append(candidates, featurePruneCandidate{Key: key, Kind: kind, Name: selected.Name, ID: id, Fingerprint: hash})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Key < candidates[j].Key })
	if len(candidates) == 0 {
		return candidates, nil
	}
	profiles, err := e.base.client.wirelessProfileInventory(ctx)
	if err != nil {
		return nil, err
	}
	for _, profile := range profiles {
		references, known := profile.fields["featureTemplates"].([]map[string]any)
		if profile.unmapped || !known {
			return nil, errFeaturePruneReferenced
		}
		for _, ref := range references {
			for _, candidate := range candidates {
				if ref["id"] == candidate.ID {
					return nil, errFeaturePruneReferenced
				}
			}
		}
	}
	return candidates, nil
}
func (e *featurePruneExecutor) next(ctx context.Context) (*configurationWrite, error) {
	normal, err := e.base.next(ctx)
	if err != nil {
		return nil, err
	}
	candidates, err := e.snapshot(ctx)
	if err != nil || len(candidates) == 0 {
		return normal, err
	}
	if len(e.base.receipts)+len(candidates) > 256 {
		return nil, errConfigurationBusy
	}
	selected := candidates[0]
	path := wirelessFRAPath
	if selected.Kind == wirelessCleanAirKind {
		path = wirelessCleanAirPath
	}
	write := &configurationWrite{resource: featureDeletePrefix + selected.Key + ":" + selected.Fingerprint, action: "Delete", identity: configurationResourceIdentity{target: selected.ID}}
	if e.resource != nil {
		write.resource = e.resource(selected)
	}
	write.submit = func(ctx context.Context) (Task, error) {

		return e.base.client.wirelessFeatureMutationTask(ctx, http.MethodDelete, path+"/"+selected.ID, nil)
	}
	guardWirelessDefinitionPreflight(write, func(ctx context.Context) error {
		current, err := e.snapshot(ctx)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(current, candidates) {
			return errConfigurationStale
		}
		if e.guard == nil {
			return errConfigurationTransition
		}
		return e.guard(ctx, write)
	})
	return write, nil
}
func (e *featurePruneExecutor) verifyDelete(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: e.base.receipts}
	if err := featureDeleteAuthority(state, op); err != nil {
		return false, err
	}
	key, _ := featureDeleteKey(op)
	if op.ConfigUID != e.base.owner || op.ConfigName != e.base.ownerName || op.SubmittedAt == nil || !validAPIID(op.TaskID) {
		return false, errConfigurationTransition
	}
	for _, binding := range e.base.bindings {
		if binding.key() == key {
			return false, errConfigurationOwned
		}
	}
	// NCNC Delete results identify the original UUID, not a design name.
	_, done, err := e.base.client.wirelessFeatureTaskResult(ctx, wirelessFeatureTaskReference{ID: op.TaskID, Name: op.TargetID, Action: "Delete", Target: op.TargetID, Submitted: op.SubmittedAt.Time}, time.Now())
	if err != nil || !done {
		return false, err
	}
	kind := featureKind(key)
	for range 2 {
		rows, err := e.base.client.wirelessFeatureInstances(ctx, kind)
		if err != nil {
			return false, err
		}
		for _, instance := range rows {
			if instance.ID == op.TargetID || featureNameKey(kind, instance.Name) == key {
				return false, nil
			}
		}
	}
	return true, nil
}
