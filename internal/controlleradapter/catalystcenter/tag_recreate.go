// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"reflect"
	"strings"
)

const tagRecreatePrefix = "tag-recreate:v1:"
const tagRetiredPrefix = "tag-retired:v1:"

func tagRecreateSelected(resource string) bool { return strings.HasPrefix(resource, tagRecreatePrefix) }
func tagRetiredSelected(resource string) bool  { return strings.HasPrefix(resource, tagRetiredPrefix) }
func tagOwnedCanonical(resource string) string {
	if tagSuccessorSelected(resource) {
		return tagSuccessorCanonical(resource)
	}
	if !tagRecreateSelected(resource) {
		return resource
	}
	parts := strings.Split(strings.TrimPrefix(resource, tagRecreatePrefix), ":")
	if len(parts) != 6 || parts[0] != "c" || !configurationOperationID.MatchString(parts[4]) || !validTemplateComparisonDigest(parts[5]) {
		return ""
	}
	return tagOwnedPrefix + strings.Join(parts[:4], ":")
}
func tagRecreateParent(resource string) (string, string, bool) {
	if !tagRecreateSelected(resource) || tagOwnedCanonical(resource) == "" {
		return "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(resource, tagRecreatePrefix), ":")
	return parts[4], parts[5], true
}
func tagReceiptsForKey(state *cvk.NetworkControllerConfigurationStatus, key string) []cvk.NetworkControllerConfigurationActionApplication {
	out := []cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return out
	}
	for _, r := range state.ActionApplications {
		if tagRetiredSelected(r.Resource) || tagOwnerArchivedSelected(r.Resource) {
			continue
		}
		current, _, ok := tagOwnedParts(&r.Operation)
		if !ok {
			current, ok = tagDeleteKey(&r.Operation)
		}
		if ok && current == key {
			out = append(out, r)
		}
	}
	return out
}

type tagRetiredCohort struct {
	Key, ID, Digest  string
	Creator, Deleted cvk.NetworkControllerConfigurationActionApplication
	Rows             []cvk.NetworkControllerConfigurationActionApplication
}

// Validate complete resource-specific cohorts while reusing the existing shared
// deterministic cohort digest and input ordering. Archives never confer adoption.
func tagLocalRetiredCohorts(state *cvk.NetworkControllerConfigurationStatus) (map[string]tagRetiredCohort, error) {
	out := map[string]tagRetiredCohort{}
	if state == nil {
		return out, nil
	}
	groups := map[string][]cvk.NetworkControllerConfigurationActionApplication{}
	seenOps := map[string]bool{}
	for _, r := range state.ActionApplications {
		if tagOwnerArchivedSelected(r.Resource) {
			continue
		}
		if !tagRetiredSelected(r.Resource) && !tagOwnedSelected(r.Operation.Resource) && !tagDeleteSelected(r.Operation.Resource) {
			continue
		}
		if !configurationOperationID.MatchString(r.Operation.ID) || seenOps[r.Operation.ID] {
			return nil, errConfigurationOwned
		}
		seenOps[r.Operation.ID] = true
		if !tagRetiredSelected(r.Resource) {
			continue
		}
		row := r
		row.Resource = strings.TrimPrefix(row.Resource, tagRetiredPrefix)
		if tagRetiredSelected(row.Resource) {
			return nil, errConfigurationOwned
		}
		key, id := "", ""
		if tagDeleteSelected(row.Operation.Resource) {
			var ok bool
			key, ok = tagDeleteKey(&row.Operation)
			id = row.Operation.TargetID
			if !ok || row.Resource != tagDeletedPrefix+key+":"+id {
				return nil, errConfigurationOwned
			}
		} else {
			var err error
			key, id, _, err = tagReceiptIdentity(row)
			if err != nil {
				return nil, err
			}
		}
		groups[key+":"+id] = append(groups[key+":"+id], row)
	}
	for _, rows := range groups {
		local := &cvk.NetworkControllerConfigurationStatus{ActionApplications: rows}
		records, err := tagOwnedRecords(local)
		if err != nil || len(records.created) != 1 {
			return nil, errConfigurationOwned
		}
		deleted, err := tagDeletedRecords(local)
		if err != nil || len(deleted) != 1 || len(rows) != 2+len(records.applied) {
			return nil, errConfigurationOwned
		}
		for key, creator := range records.created {
			tombstone, exists := deleted[key]
			if !exists {
				return nil, errConfigurationOwned
			}
			_, id, _, err := tagReceiptIdentity(creator)
			if err != nil {
				return nil, err
			}
			if _, exists := out[tombstone.Operation.ID]; exists {
				return nil, errConfigurationOwned
			}
			out[tombstone.Operation.ID] = tagRetiredCohort{Key: key, ID: id, Digest: featureCohortDigest(rows), Creator: creator, Deleted: tombstone, Rows: rows}
		}
	}
	records, err := tagOwnedRecords(state)
	if err != nil {
		return nil, err
	}
	visited := map[string]bool{}
	currentIDs := map[string]bool{}
	for key, creator := range records.created {
		_, id, _, err := tagReceiptIdentity(creator)
		if err != nil {
			return nil, err
		}
		if currentIDs[id] {
			return nil, errConfigurationOwned
		}
		currentIDs[id] = true
		for _, cohort := range out {
			if cohort.ID == id {
				return nil, errConfigurationOwned
			}
		}
		current := creator
		path := map[string]bool{}
		for tagRecreateSelected(current.Operation.Resource) {
			parent, digest, ok := tagRecreateParent(current.Operation.Resource)
			cohort, exists := out[parent]
			if !ok || !exists || path[parent] || cohort.Key != key || digest != cohort.Digest || !configurationInputAfter(&current.Operation, &cohort.Deleted.Operation) || current.Section != cohort.Deleted.Section || current.Operation.ConfigName != cohort.Deleted.Operation.ConfigName || current.Operation.ControllerGeneration < cohort.Deleted.Operation.ControllerGeneration || current.Operation.SubmittedAt == nil || current.Operation.SubmittedAt.Before(cohort.Deleted.VerifiedAt) {
				return nil, errConfigurationOwned
			}
			_, currentID, _, err := tagReceiptIdentity(current)
			if err != nil || currentID == cohort.ID {
				return nil, errConfigurationOwned
			}
			visited[parent] = true
			path[parent] = true
			current = cohort.Creator
		}
	}
	if len(visited) != len(out) {
		return nil, errConfigurationOwned
	}
	return out, nil
}
func tagRecreateAuthority(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	key, create, ok := tagOwnedParts(op)
	if !ok || !create || !tagRecreateSelected(op.Resource) {
		return errConfigurationTransition
	}
	if _, err := tagRetiredCohorts(state); err != nil {
		return err
	}
	deleted, err := tagDeletedRecords(state)
	if err != nil {
		return err
	}
	tombstone, exists := deleted[key]
	parent, digest, valid := tagRecreateParent(op.Resource)
	if !exists || !valid || parent != tombstone.Operation.ID || digest != featureCohortDigest(tagReceiptsForKey(state, key)) || !configurationInputAfter(op, &tombstone.Operation) || op.ConfigName != tombstone.Operation.ConfigName || op.Section != tombstone.Section || op.ControllerGeneration < tombstone.Operation.ControllerGeneration || op.SubmittedAt != nil && op.SubmittedAt.Before(tombstone.VerifiedAt) {
		return errConfigurationOwned
	}
	return nil
}
func tagRecreateIntent(b tagOwnedBinding, state *cvk.NetworkControllerConfigurationStatus) string {
	deleted, err := tagDeletedRecords(state)
	if err != nil {
		return ""
	}
	tombstone, exists := deleted[b.key]
	if !exists {
		return tagSuccessorIntent(b.create, state)
	}
	return tagRecreatePrefix + strings.TrimPrefix(b.create, tagOwnedPrefix) + ":" + tombstone.Operation.ID + ":" + featureCohortDigest(tagReceiptsForKey(state, b.key))
}
func (e *tagOwnedExecutor) recreateProbe(resource string) *cvk.NetworkControllerConfigurationOperation {
	return &cvk.NetworkControllerConfigurationOperation{Section: e.section, Resource: resource, Action: "Create", ConfigUID: e.owner, ConfigName: e.name, ConfigGeneration: e.configGeneration, ControllerGeneration: e.controllerGeneration, SourceUID: e.sourceUID, SourceVersion: e.sourceVersion}
}
func (e *tagOwnedExecutor) recreationAbsent(ctx context.Context, b tagOwnedBinding) error {
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}
	if err := tagRecreateAuthority(state, e.recreateProbe(tagRecreateIntent(b, state))); err != nil {
		return err
	}
	cohorts, err := tagRetiredCohorts(state)
	if err != nil {
		return err
	}
	deleted, err := tagDeletedRecords(state)
	if err != nil {
		return err
	}
	ids := map[string]bool{deleted[b.key].Operation.TargetID: true}
	for _, cohort := range cohorts {
		ids[cohort.ID] = true
	}
	rows, err := e.client.listTags(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Name == b.name || ids[row.ID] {
			return errConfigurationOwned
		}
	}
	return nil
}
func tagRecreatedIDUnused(state *cvk.NetworkControllerConfigurationStatus, id string) error {
	g, err := tagOwnerArchiveGraph(state)
	if err != nil {
		return err
	}
	if g.ids[id] {
		return errConfigurationOwned
	}
	cohorts, err := tagRetiredCohorts(state)
	if err != nil {
		return err
	}
	for _, cohort := range cohorts {
		if cohort.ID == id {
			return errConfigurationOwned
		}
	}
	records, err := tagOwnedRecords(state)
	if err != nil {
		return err
	}
	for _, r := range records.created {
		_, old, _, err := tagReceiptIdentity(r)
		if err != nil || old == id {
			return errConfigurationOwned
		}
	}
	return nil
}
func archiveTagCohort(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if err := tagRecreateAuthority(state, op); err != nil {
		return err
	}
	key, _, _ := tagOwnedParts(op)
	originals := tagReceiptsForKey(state, key)
	for i := range state.ActionApplications {
		for _, old := range originals {
			if reflect.DeepEqual(state.ActionApplications[i], old) {
				state.ActionApplications[i].Resource = tagRetiredPrefix + old.Resource
				break
			}
		}
	}
	return nil
}
