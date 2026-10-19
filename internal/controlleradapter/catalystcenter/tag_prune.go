// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
)

const tagDeletePrefix = "tag-delete:v1:"
const tagDeletedPrefix = "tag-deleted:v1:"

func tagDeleteSelected(resource string) bool {
	return strings.HasPrefix(resource, tagDeletePrefix) || tagFinalizeSelected(resource)
}
func tagDeleteKey(op *cvk.NetworkControllerConfigurationOperation) (string, bool) {
	if op == nil || !tagDeleteSelected(op.Resource) || op.Action != "Delete" {
		return "", false
	}
	copy := *op
	copy.Resource = tagOwnedPrefix + "u:" + strings.TrimPrefix(tagDeleteCanonical(op.Resource), tagDeletePrefix)
	copy.Action = "Update"
	key, _, ok := tagOwnedParts(&copy)
	return key, ok
}
func validTagDeleteOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	_, ok := tagDeleteKey(op)
	return ok
}
func tagPruneSource(raw []byte, sections []string) bool {
	if !(len(sections) == 1 && (sections[0] == "inventory" || sections[0] == "templates") || combinedTagSections(sections)) {
		return false
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return false
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != len(sections) {
		return false
	}
	for _, section := range sections {
		body, ok := root[section].(map[string]any)
		if !ok || len(body) > 1 {
			return false
		}
		for key, value := range body {
			if key != "tags" {
				return false
			}
			if _, err := parseTemplateTags(value); err != nil {
				return false
			}
		}
	}
	return true
}
func tagOwnershipHistory(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) bool {
	if nc == nil || nc.Status.Configuration == nil {
		return false
	}
	for _, r := range nc.Status.Configuration.ActionApplications {
		if r.Operation.ConfigUID == string(c.UID) && (tagOwnedSelected(r.Operation.Resource) || tagDeleteSelected(r.Operation.Resource) || strings.HasPrefix(r.Resource, tagCreatedPrefix) || strings.HasPrefix(r.Resource, tagDeletedPrefix)) {
			return true
		}
	}
	return tagOwnerCustodyOwner(nc, c)
}
func tagFullReceiptState(r cvk.NetworkControllerConfigurationActionApplication) (string, error) {
	if _, _, _, err := tagReceiptIdentity(r); err != nil {
		return "", err
	}
	parts := strings.Split(r.Resource, ":")
	if len(parts) != 7 || parts[5] != "s" || !validTemplateComparisonDigest(parts[6]) {
		return "", errConfigurationOwned
	}
	return parts[6], nil
}
func tagDeleteAuthority(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	key, ok := tagDeleteKey(op)
	if !ok {
		return errConfigurationTransition
	}
	records, err := tagOwnedRecords(state)
	if err != nil {
		return err
	}
	latest, exists := records.created[key]
	if !exists {
		return errConfigurationOwned
	}
	if r, updated := records.applied[key]; updated {
		latest = r
	}
	_, id, _, err := tagReceiptIdentity(latest)
	full, fullErr := tagFullReceiptState(latest)
	parts := strings.Split(strings.TrimPrefix(tagDeleteCanonical(op.Resource), tagDeletePrefix), ":")
	if err != nil || fullErr != nil || op.TargetID != id || parts[1] != full || op.Section != latest.Section || op.ConfigUID != latest.Operation.ConfigUID || op.ConfigName != latest.Operation.ConfigName || op.ConfigGeneration < latest.Operation.ConfigGeneration || op.ControllerGeneration < latest.Operation.ControllerGeneration || op.SubmittedAt != nil && op.SubmittedAt.Before(latest.VerifiedAt) {
		return errConfigurationOwned
	}
	if validAPIID(op.TaskID) && state != nil {
		for _, r := range state.ActionApplications {
			if r.Operation.TaskID == op.TaskID && r.Operation.ID != op.ID {
				return errConfigurationOwned
			}
		}
	}
	return nil
}
func tagDeletedRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	out := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	seenOperations := map[string]bool{}
	if state == nil {
		return out, nil
	}
	for _, r := range state.ActionApplications {
		if tagRetiredSelected(r.Resource) || tagOwnerArchivedSelected(r.Resource) {
			continue
		}
		if !tagDeleteSelected(r.Operation.Resource) && !strings.HasPrefix(r.Resource, tagDeletedPrefix) {
			continue
		}
		op := &r.Operation
		key, ok := tagDeleteKey(op)
		if !ok || r.Resource != tagDeletedPrefix+key+":"+op.TargetID || r.Section != op.Section || op.Phase != "Verified" || !configurationOperationID.MatchString(op.ID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) || tagDeleteAuthority(state, op) != nil {
			return nil, errConfigurationOwned
		}
		if seenOperations[op.ID] {
			return nil, errConfigurationOwned
		}
		seenOperations[op.ID] = true
		if _, exists := out[key]; exists {
			return nil, errConfigurationOwned
		}
		out[key] = r
	}
	return out, nil
}
func tagDeleteCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if _, err := tagRetiredCohorts(state); err != nil {
		return err
	}
	if err := tagDeleteAuthority(state, op); err != nil {
		return err
	}
	deleted, err := tagDeletedRecords(state)
	if err != nil {
		return err
	}
	key, _ := tagDeleteKey(op)
	if _, exists := deleted[key]; exists {
		return errConfigurationOwned
	}
	if len(state.ActionApplications) >= 256 {
		return errConfigurationBusy
	}
	raw, err := json.Marshal(state)
	if err != nil || len(raw) > 250<<10 {
		return errConfigurationBusy
	}
	return nil
}
func tagDeletedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if _, err := tagRetiredCohorts(state); err != nil {
		return false
	}
	key, ok := tagDeleteKey(op)
	if !ok || op.Phase != "Verified" {
		return false
	}
	rows, err := tagDeletedRecords(state)
	if err != nil || !reflect.DeepEqual(rows[key].Operation, *op) {
		return false
	}
	count := 0
	for _, r := range state.ActionApplications {
		if r.Operation.ID == op.ID {
			count++
		}
	}
	return count == 1
}
func (e *tagOwnedExecutor) validatePruneScope(nc *cvk.NetworkController, c *configapi.NetworkControllerConfig) error {
	if c.Spec.DeletionPolicy == configapi.NetworkControllerRetentionPolicyDelete && !e.cleanupEnrolled || len(c.Spec.SecretRefs) != 0 {
		return errConfigurationTransition
	}
	if nc.Status.Configuration == nil {
		return nil
	}
	for _, r := range nc.Status.Configuration.CredentialApplications {
		if r.ConfigUID == e.owner {
			return errConfigurationOwned
		}
	}
	for _, r := range e.receipts {
		if r.Operation.ConfigUID == e.owner && !tagOwnedSelected(r.Operation.Resource) && !tagDeleteSelected(r.Operation.Resource) {
			return errConfigurationOwned
		}
	}
	_, err := tagDeletedRecords(nc.Status.Configuration)
	return err
}
func (e *tagOwnedExecutor) wantsDeleteKey(op *cvk.NetworkControllerConfigurationOperation) bool {
	key, ok := tagDeleteKey(op)
	if !ok {
		return true
	}
	for _, b := range e.bindings {
		if b.key == key {
			return true
		}
	}
	return false
}

type tagPruneCandidate struct {
	Key, ID, Name, Full string
	Types               []string
}

func (e *tagOwnedExecutor) pruneSnapshot(ctx context.Context) ([]tagPruneCandidate, error) {
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}
	records, err := tagOwnedRecords(state)
	if err != nil {
		return nil, err
	}
	deleted, err := tagDeletedRecords(state)
	if err != nil {
		return nil, err
	}
	rows, err := e.client.listTags(ctx)
	if err != nil {
		return nil, err
	}
	if err := tagOwnerArchiveNativeAbsent(state, rows); err != nil {
		return nil, err
	}
	wanted := map[string]bool{}
	for _, b := range e.bindings {
		wanted[b.key] = true
	}
	candidates := []tagPruneCandidate{}
	for key, creator := range records.created {
		if creator.Operation.ConfigUID != e.owner {
			continue
		}
		if creator.Operation.ConfigName != e.name {
			return nil, errConfigurationOwned
		}
		if wanted[key] {
			continue
		}
		_, id, _, err := tagReceiptIdentity(creator)
		if err != nil {
			return nil, err
		}
		var selected *observedTag
		for _, row := range rows {
			if row.ID == id || featureIdentityDigest(row.Name) == key {
				if selected != nil || row.ID != id || featureIdentityDigest(row.Name) != key || row.SystemTag == nil || *row.SystemTag {
					return nil, errConfigurationOwned
				}
				copy := row
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
		}
		latest := creator
		if applied, ok := records.applied[key]; ok {
			latest = applied
		}
		full, err := tagFullReceiptState(latest)
		if err != nil {
			return nil, err
		}
		detail, observed, err := e.client.tagNativeState(ctx, id)
		if err != nil {
			return nil, err
		}
		if observed != full || detail.Name != selected.Name || !reflect.DeepEqual(tagPayload(detail), tagPayload(*selected)) {
			return nil, errConfigurationOwned
		}
		types, err := e.client.tagUnused(ctx, id)
		if err != nil {
			return nil, err
		}
		_, again, err := e.client.tagNativeState(ctx, id)
		if err != nil {
			return nil, err
		}
		if again != full {
			return nil, errConfigurationOwned
		}
		candidates = append(candidates, tagPruneCandidate{Key: key, ID: id, Name: selected.Name, Full: full, Types: types})
	}
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].Key < candidates[j].Key })
	if len(e.receipts)+len(candidates) > 256 {
		return nil, errConfigurationBusy
	}
	return candidates, nil
}
func (e *tagOwnedExecutor) pruneNext(ctx context.Context) (*configurationWrite, error) {
	candidates, err := e.pruneSnapshot(ctx)
	if err != nil || len(candidates) == 0 {
		return nil, err
	}
	selected := candidates[0]
	write := &configurationWrite{resource: tagDeletePrefix + selected.Key + ":" + selected.Full + ":" + e.sourceMode, action: "Delete", identity: configurationResourceIdentity{target: selected.ID}}
	if e.finalizeResource != nil {
		write.resource = e.finalizeResource(selected)
	}
	write.submit = func(ctx context.Context) (Task, error) {
		raw, err := e.client.mutate(ctx, http.MethodDelete, tagsPath+"/"+url.PathEscape(selected.ID), nil)
		if err != nil {
			return Task{}, err
		}
		var env struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &env) != nil || !validAPIID(env.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return env.Response, nil
	}
	guardConfigurationPreflight(write, func(ctx context.Context) error {
		fresh, err := e.pruneSnapshot(ctx)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(fresh, candidates) {
			return errConfigurationStale
		}
		if e.pruneGuard == nil {
			return errConfigurationTransition
		}
		return e.pruneGuard(ctx, write)
	})
	return write, nil
}
func (e *tagOwnedExecutor) verifyDelete(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if !e.prune || e.wantsDeleteKey(op) || tagDeleteAuthority(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, op) != nil || op.ConfigUID != e.owner || op.ConfigName != e.name || op.SubmittedAt == nil || !validAPIID(op.TaskID) {
		return false, errConfigurationOwned
	}
	done, err := e.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	if err != nil || !done {
		return false, err
	}
	key, _ := tagDeleteKey(op)
	for range 2 {
		rows, err := e.client.listTags(ctx)
		if err != nil {
			return false, err
		}
		for _, row := range rows {
			if row.ID == op.TargetID || featureIdentityDigest(row.Name) == key {
				return false, errConfigurationOwned
			}
		}
	}
	return true, nil
}
