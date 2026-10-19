// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const featureRecreatePrefix = "feature-recreate:v1:"
const featureRetiredPrefix = "feature-retired:v1:"

func featureRecreateSelected(resource string) bool {
	return strings.HasPrefix(resource, featureRecreatePrefix)
}
func featureRetiredSelected(resource string) bool {
	return strings.HasPrefix(resource, featureRetiredPrefix)
}
func featureCreateCanonical(resource string) string {
	if !featureRecreateSelected(resource) {
		return resource
	}
	parts := strings.Split(strings.TrimPrefix(resource, featureRecreatePrefix), ":")
	if len(parts) != 5 || !configurationOperationID.MatchString(parts[3]) || !validWirelessFeatureFingerprint(parts[4]) {
		return ""
	}
	return featureCreatePrefix + strings.Join(parts[:3], ":")
}
func featureRecreateParent(resource string) (string, string, bool) {
	if !featureRecreateSelected(resource) || featureCreateCanonical(resource) == "" {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(resource, featureRecreatePrefix), ":")
	return parts[3], parts[4], true
}
func featureCohortDigest(rows []cvk.NetworkControllerConfigurationActionApplication) string {
	copy := append([]cvk.NetworkControllerConfigurationActionApplication{}, rows...)
	sort.Slice(copy, func(i, j int) bool { return copy[i].Resource < copy[j].Resource })
	return featureIdentityDigest(copy)
}

type featureRetiredCohort struct {
	Key, ID, Digest  string
	Creator, Deleted cvk.NetworkControllerConfigurationActionApplication
	Rows             []cvk.NetworkControllerConfigurationActionApplication
}

// Archive validation reconstructs each complete retired lifecycle and walks the
// predecessor chain from current creators. Every archive must be reachable, and
// every new creator binds the exact predecessor cohort digest, including its
// optional applied record. A missing row cannot silently shorten that history.
func featureRetiredCohorts(state *cvk.NetworkControllerConfigurationStatus) (map[string]featureRetiredCohort, error) {
	cohorts := map[string]featureRetiredCohort{}
	if state == nil {
		return cohorts, nil
	}
	groups := map[string][]cvk.NetworkControllerConfigurationActionApplication{}
	seenIDs := map[string]bool{}
	for _, row := range state.ActionApplications {
		// Feature lineage must not impose new validation rules on other resource
		// families sharing the action store. Their own protocols validate them.
		if !featureRetiredSelected(row.Resource) && !featureCreateSelected(row.Operation.Resource) && !featureUpdateSelected(row.Operation.Resource) && !featureDeleteSelected(row.Operation.Resource) {
			continue
		}
		if seenIDs[row.Operation.ID] && configurationOperationID.MatchString(row.Operation.ID) {
			return nil, errConfigurationOwned
		}
		seenIDs[row.Operation.ID] = true
		if !featureRetiredSelected(row.Resource) {
			continue
		}
		original := row
		original.Resource = strings.TrimPrefix(row.Resource, featureRetiredPrefix)
		op := &original.Operation
		key, ok := featureCreateKey(op.Resource)
		prefix := featureCreatedPrefix
		if !ok {
			key, ok = featureUpdateKey(op)
			prefix = featureAppliedPrefix
		}
		if !ok {
			key, ok = featureDeleteKey(op)
			prefix = featureDeletedPrefix
		}
		if !ok {
			return nil, errConfigurationOwned
		}
		id, _, err := featureReceiptIdentity(original.Resource, prefix, key)
		if err != nil {
			return nil, err
		}
		group := key + ":" + id
		groups[group] = append(groups[group], original)
	}
	for _, rows := range groups {
		local := &cvk.NetworkControllerConfigurationStatus{ActionApplications: rows}
		creators, err := featureCreatedRecords(local)
		if err != nil || len(creators) != 1 {
			return nil, errConfigurationOwned
		}
		applied, err := featureAppliedRecords(local)
		if err != nil {
			return nil, err
		}
		deleted, err := featureDeletedRecords(local)
		if err != nil || len(deleted) != 1 || len(rows) != 2+len(applied) {
			return nil, errConfigurationOwned
		}
		for key, creator := range creators {
			tombstone, exists := deleted[key]
			if !exists {
				return nil, errConfigurationOwned
			}
			id, err := featureCreatedTarget(creator)
			if err != nil {
				return nil, err
			}
			if _, duplicate := cohorts[tombstone.Operation.ID]; duplicate {
				return nil, errConfigurationOwned
			}
			cohorts[tombstone.Operation.ID] = featureRetiredCohort{Key: key, ID: id, Digest: featureCohortDigest(rows), Creator: creator, Deleted: tombstone, Rows: rows}
		}
	}
	creators, err := featureCreatedRecords(state)
	if err != nil {
		return nil, err
	}
	visited := map[string]bool{}
	for key, creator := range creators {
		current := creator
		path := map[string]bool{}
		id, err := featureCreatedTarget(current)
		if err != nil {
			return nil, err
		}
		for _, cohort := range cohorts {
			if cohort.Key == key && cohort.ID == id {
				return nil, errConfigurationOwned
			}
		}
		for featureRecreateSelected(current.Operation.Resource) {
			parent, digest, ok := featureRecreateParent(current.Operation.Resource)
			cohort, exists := cohorts[parent]
			if !ok || !exists || path[parent] || cohort.Key != key || digest != cohort.Digest || !configurationInputAfter(&current.Operation, &cohort.Deleted.Operation) || current.Operation.ConfigName != cohort.Deleted.Operation.ConfigName || current.Operation.ControllerGeneration < cohort.Deleted.Operation.ControllerGeneration || current.Operation.SubmittedAt == nil || current.Operation.SubmittedAt.Before(cohort.Deleted.VerifiedAt) {
				return nil, errConfigurationOwned
			}
			currentID, err := featureCreatedTarget(current)
			if err != nil || currentID == cohort.ID {
				return nil, errConfigurationOwned
			}
			visited[parent] = true
			path[parent] = true
			current = cohort.Creator
		}
	}
	if len(visited) != len(cohorts) {
		return nil, errConfigurationOwned
	}
	return cohorts, nil
}
func featureRecreateAuthority(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if !validFeatureCreateOperation(op) || !featureRecreateSelected(op.Resource) {
		return errConfigurationTransition
	}
	if _, err := featureRetiredCohorts(state); err != nil {
		return err
	}
	deleted, err := featureDeletedRecords(state)
	if err != nil {
		return err
	}
	key, _ := featureCreateKey(op.Resource)
	tombstone, exists := deleted[key]
	parent, digest, ok := featureRecreateParent(op.Resource)
	if !exists || !ok || parent != tombstone.Operation.ID || digest != featureCohortDigest(featureReceiptsForKey(state, key)) || !configurationInputAfter(op, &tombstone.Operation) || op.ConfigName != tombstone.Operation.ConfigName || op.ControllerGeneration < tombstone.Operation.ControllerGeneration || op.SubmittedAt != nil && op.SubmittedAt.Before(tombstone.VerifiedAt) {
		return errConfigurationOwned
	}
	return nil
}
func featureRecreateIntent(binding featureCreateBinding, state *cvk.NetworkControllerConfigurationStatus) string {
	deleted, err := featureDeletedRecords(state)
	if err != nil {
		return ""
	}
	tombstone, exists := deleted[binding.key()]
	if !exists {
		return binding.resource
	}
	return featureRecreatePrefix + strings.TrimPrefix(binding.resource, featureCreatePrefix) + ":" + tombstone.Operation.ID + ":" + featureCohortDigest(featureReceiptsForKey(state, binding.key()))
}
func (e *featureOwnedCreateExecutor) recreateProbe(resource string) *cvk.NetworkControllerConfigurationOperation {
	return &cvk.NetworkControllerConfigurationOperation{Section: "feature_templates", Resource: resource, Action: "Create", ConfigUID: e.owner, ConfigName: e.ownerName, ConfigGeneration: e.configGeneration, ControllerGeneration: e.controllerGeneration, SourceUID: e.sourceUID, SourceVersion: e.sourceVersion}
}
func (e *featureOwnedCreateExecutor) recreationAbsent(ctx context.Context, b featureCreateBinding) error {
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}
	resource := featureRecreateIntent(b, state)
	if err := featureRecreateAuthority(state, e.recreateProbe(resource)); err != nil {
		return err
	}
	cohorts, err := featureRetiredCohorts(state)
	if err != nil {
		return err
	}
	deleted, err := featureDeletedRecords(state)
	if err != nil {
		return err
	}
	ids := map[string]bool{deleted[b.key()].Operation.TargetID: true}
	for _, cohort := range cohorts {
		if cohort.Key == b.key() {
			ids[cohort.ID] = true
		}
	}
	rows, err := e.client.wirelessFeatureInstances(ctx, b.kind)
	if err != nil {
		return err
	}
	for _, instance := range rows {
		if instance.Name == b.name || ids[instance.ID] {
			return errConfigurationOwned
		}
	}
	return nil
}
func featureRecreatedIDUnused(state *cvk.NetworkControllerConfigurationStatus, key, id string) error {
	cohorts, err := featureRetiredCohorts(state)
	if err != nil {
		return err
	}
	deleted, err := featureDeletedRecords(state)
	if err != nil {
		return err
	}
	if row, exists := deleted[key]; exists && row.Operation.TargetID == id {
		return errConfigurationOwned
	}
	for _, cohort := range cohorts {
		if cohort.Key == key && cohort.ID == id {
			return errConfigurationOwned
		}
	}
	return nil
}
func archiveFeatureCohort(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if err := featureRecreateAuthority(state, op); err != nil {
		return err
	}
	key, _ := featureCreateKey(op.Resource)
	originals := featureReceiptsForKey(state, key)
	for index := range state.ActionApplications {
		row := &state.ActionApplications[index]
		for _, original := range originals {
			if reflect.DeepEqual(*row, original) {
				row.Resource = featureRetiredPrefix + row.Resource
				break
			}
		}
	}
	return nil
}
