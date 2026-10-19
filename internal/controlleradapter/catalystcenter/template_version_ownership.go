// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/hmac"
	"encoding/json"
	"fmt"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const templateVersionOwnershipTag = "version-v1"
const templateAppliedHistoryPrefix = "template-applied-history:v1:"

func templateVersionOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	_, _, stage, ok := templateApplyIdentity(op)
	return ok && stage == "commit" && strings.HasSuffix(op.Resource, ":"+templateVersionOwnershipTag)
}
func templateCommitComment(base, id string) string {
	return base + " [cvk:" + id + "]"
}
func templateCommitCommentMatches(got, want string) bool {
	if got == want {
		return true
	}
	id, ok := strings.CutPrefix(got, want+" [cvk:")
	return ok && strings.HasSuffix(id, "]") && configurationOperationID.MatchString(strings.TrimSuffix(id, "]"))
}
func templateVersionCommentOperation(comment string) string {
	_, tail, ok := strings.Cut(comment, " [cvk:")
	if !ok || !strings.HasSuffix(tail, "]") {
		return ""
	}
	id := strings.TrimSuffix(tail, "]")
	if !configurationOperationID.MatchString(id) {
		return ""
	}
	return id
}
func validTemplateAppliedTail(op *cvk.NetworkControllerConfigurationOperation, tail []string) bool {
	if templateVersionOperation(op) {
		return len(tail) == 5 && tail[2] == templateVersionOwnershipTag && canonicalTemplateUUID(tail[3]) && validTemplateComparisonDigest(tail[4])
	}
	return len(tail) == 2
}
func templateAppliedVersionResult(op *cvk.NetworkControllerConfigurationOperation, original cvk.NetworkControllerConfigurationActionApplication, mac, id, bodyMAC string) string {
	if !templateVersionOperation(op) || !canonicalTemplateUUID(id) || !validTemplateComparisonDigest(bodyMAC) {
		return ""
	}
	return templateAppliedResult(op, original, mac) + ":" + templateVersionOwnershipTag + ":" + id + ":" + bodyMAC
}

// Archived commit receipts remain immutable and linked to their original creator.
// They are observations of version UUIDs, never native version Delete permits.
func templateAppliedHistoryRecords(state *cvk.NetworkControllerConfigurationStatus) ([]cvk.NetworkControllerConfigurationActionApplication, error) {
	out := []cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return out, nil
	}
	creators, err := templateCreatedRecords(state)
	if err != nil {
		return nil, err
	}
	for _, r := range state.ActionApplications {
		if !strings.HasPrefix(r.Resource, templateAppliedHistoryPrefix) {
			continue
		}
		if !templateVersionOperation(&r.Operation) {
			return nil, errConfigurationOwned
		}
		row := *r.DeepCopy()
		row.Resource = templateAppliedPrefix + strings.TrimPrefix(r.Resource, templateAppliedHistoryPrefix)
		key, _, _, _ := templateApplyIdentity(&row.Operation)
		original, exists := creators[key]
		if !exists {
			return nil, errConfigurationOwned
		}
		_, err := templateAppliedCurrentRecords(&cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{original, row}})
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func templateAppliedRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	current, err := templateAppliedCurrentRecords(state)
	if err != nil {
		return nil, err
	}
	history, err := templateAppliedHistoryRecords(state)
	if err != nil {
		return nil, err
	}
	seenUUID, seenOp := map[string]bool{}, map[string]bool{}
	all := append([]cvk.NetworkControllerConfigurationActionApplication(nil), history...)
	for _, r := range current {
		if templateVersionOperation(&r.Operation) {
			all = append(all, r)
		}
	}
	for _, r := range all {
		parts := strings.Split(r.Resource, ":")
		id := parts[len(parts)-2]
		if seenUUID[id] || seenOp[r.Operation.ID] {
			return nil, errConfigurationOwned
		}
		seenUUID[id], seenOp[r.Operation.ID] = true, true
	}
	return current, nil
}

func templateVersionBodyMAC(comparison *templateInputComparison, resource, id string, body any) (string, error) {
	return templateNativeMAC(comparison, resource, map[string]any{"versionId": id, "committed": body})
}

// Require exactly one committed native version carrying this operation's nonce,
// independently checked body/parent/version identity and full original task tree.
// No free-form task data or progress is interpreted as an identity.
func (e *templateOwnedExecutor) captureCreatedVersion(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (string, string, error) {
	b, stage, ok := e.findApplied(op)
	if !ok || stage != "commit" || !templateVersionOperation(op) || !configurationOperationID.MatchString(op.ID) {
		return "", "", errConfigurationTransition
	}
	_, creatorResource, _, _ := templateApplyIdentity(op)
	if e.comparison == nil || e.comparison.check == nil {
		return "", "", errConfigurationSecret
	}
	if err := e.comparison.check(ctx); err != nil {
		return "", "", err
	}
	footprint, err := e.client.templateVersionFootprint(ctx, templateSummary{ID: op.TargetID, Name: b.name, ProjectID: op.ParentID})
	if err != nil {
		return "", "", err
	}
	bodies, err := templateVersionBodies(footprint)
	if err != nil {
		return "", "", err
	}
	entries, err := templateVersionEntries(footprint, op.TargetID)
	if err != nil {
		return "", "", err
	}
	id := ""
	var chosen any
	for version, raw := range bodies {
		body, ok := raw.(map[string]any)
		if !ok {
			return "", "", errInvalidResponse
		}
		entry, exists := entries[version]
		if !exists {
			return "", "", errInvalidResponse
		}
		comment, _ := entry["versionComment"].(string)
		if declared, exists := body["versionComment"]; exists && declared != comment {
			return "", "", errInvalidResponse
		}
		if templateVersionCommentOperation(comment) != op.ID {
			continue
		}
		if id != "" || !canonicalTemplateUUID(version) {
			return "", "", errConfigurationOwned
		}
		id, chosen = version, body
	}
	if id == "" {
		return "", "", errConfigurationBusy
	}
	object := footprint.(map[string]any)
	metadata, err := json.Marshal(object["metadata"])
	if err != nil {
		return "", "", errInvalidResponse
	}
	latest, err := decodeLatestTemplateVersion(metadata, op.TargetID)
	if err != nil {
		return "", "", err
	}
	if latest == nil || latest.ID != id {
		return "", "", errConfigurationOwned
	}
	body := chosen.(map[string]any)
	if b.snapshotMatches == nil {
		return "", "", errConfigurationTransition
	}
	if err := b.snapshotMatches(ctx, body); err != nil {
		return "", "", err
	}
	mac, err := templateVersionBodyMAC(e.comparison, creatorResource, id, map[string]any{"definition": chosen, "metadata": entries[id]})
	if err != nil {
		return "", "", err
	}
	if err := e.comparison.check(ctx); err != nil {
		return "", "", err
	}
	return id, mac, nil
}
func templateVersionBodies(footprint any) (map[string]any, error) {
	object, ok := footprint.(map[string]any)
	if !ok {
		return nil, errInvalidResponse
	}
	bodies, ok := object["committed"].(map[string]any)
	if !ok {
		return nil, errInvalidResponse
	}
	return bodies, nil
}

// A native version set is owned only if every current UUID has its original
// nonce-bound commit receipt and unchanged keyed committed body. Later applied
// snapshots cannot adopt an external version or rebaseline an older body edit.
func templateVersionCohortMatches(comparison *templateInputComparison, state *cvk.NetworkControllerConfigurationStatus, creatorResource string, footprint any) (bool, error) {
	key, ok := templateCreateKey(creatorResource)
	if !ok || comparison == nil || comparison.keyID != templateCreatorComparisonKey(creatorResource) {
		return false, errConfigurationSecret
	}
	creators, err := templateCreatedRecords(state)
	if err != nil {
		return false, err
	}
	original, exists := creators[key]
	if !exists || templateCreatorComparisonKey(original.Operation.Resource) != comparison.keyID {
		return false, errConfigurationOwned
	}
	current, err := templateAppliedRecords(state)
	if err != nil {
		return false, err
	}
	history, err := templateAppliedHistoryRecords(state)
	if err != nil {
		return false, err
	}
	rows := append([]cvk.NetworkControllerConfigurationActionApplication(nil), history...)
	if row, exists := current[key]; exists && templateVersionOperation(&row.Operation) {
		rows = append(rows, row)
	}
	owned := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	for _, r := range rows {
		selected, resource, _, _ := templateApplyIdentity(&r.Operation)
		if selected != key {
			continue
		}
		if templateCreatorComparisonKey(resource) != comparison.keyID {
			return false, errConfigurationSecret
		}
		parts := strings.Split(r.Resource, ":")
		owned[parts[len(parts)-2]] = r
	}
	baseID, err := templateCreatedTarget(original)
	if err != nil {
		return false, err
	}
	bodies, err := templateVersionBodies(footprint)
	if err != nil {
		return false, err
	}
	entries, err := templateVersionEntries(footprint, baseID)
	if err != nil {
		return false, err
	}
	if len(entries) != len(bodies) {
		return false, errInvalidResponse
	}
	if len(bodies) != len(owned) {
		return false, nil
	}
	for id, body := range bodies {
		row, exists := owned[id]
		if !exists {
			return false, nil
		}
		parts := strings.Split(row.Resource, ":")
		expected := parts[len(parts)-1]
		entry, exists := entries[id]
		if !exists {
			return false, errInvalidResponse
		}
		actual, err := templateVersionBodyMAC(comparison, creatorResource, id, map[string]any{"definition": body, "metadata": entry})
		if err != nil {
			return false, err
		}
		if !hmac.Equal([]byte(expected), []byte(actual)) {
			return false, nil
		}
		fields, ok := body.(map[string]any)
		if !ok {
			return false, errInvalidResponse
		}
		comment, _ := entry["versionComment"].(string)
		if fields["id"] != id || fields["projectId"] != original.Operation.ParentID {
			return false, errConfigurationOwned
		}
		if declared, exists := fields["versionComment"]; exists && declared != comment {
			return false, errInvalidResponse
		}
		if templateVersionCommentOperation(comment) != row.Operation.ID {
			return false, nil
		}
	}
	return true, nil
}

func templateVersionCapacityPlaceholder(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation, original cvk.NetworkControllerConfigurationActionApplication) string {
	usedID, usedVersion := map[string]bool{}, map[string]bool{}
	for _, r := range state.ActionApplications {
		usedID[r.Operation.ID] = true
		if templateVersionOperation(&r.Operation) {
			parts := strings.Split(r.Resource, ":")
			if len(parts) >= 2 {
				usedVersion[parts[len(parts)-2]] = true
			}
		}
	}
	for n := 1; ; n++ {
		id := fmt.Sprintf("%032x", n)
		if !usedID[id] {
			op.ID = id
			break
		}
	}
	version := ""
	for n := 1; ; n++ {
		id := fmt.Sprintf("00000000-0000-4000-8000-%012x", n)
		if !usedVersion[id] {
			version = id
			break
		}
	}
	return templateAppliedVersionResult(op, original, strings.Repeat("a", 64), version, strings.Repeat("b", 64))
}

func templateVersionEntries(footprint any, baseID string) (map[string]map[string]any, error) {
	object, ok := footprint.(map[string]any)
	if !ok {
		return nil, errInvalidResponse
	}
	raw, err := json.Marshal(object["metadata"])
	if err != nil {
		return nil, errInvalidResponse
	}
	if _, err := decodeLatestTemplateVersion(raw, baseID); err != nil {
		return nil, err
	}
	var groups []struct {
		Entries []json.RawMessage `json:"versionsInfo"`
	}
	if json.Unmarshal(raw, &groups) != nil {
		return nil, errInvalidResponse
	}
	out := map[string]map[string]any{}
	for _, group := range groups {
		for _, encoded := range group.Entries {
			decoded, err := decodeTemplateNativeJSON(encoded)
			if err != nil {
				return nil, err
			}
			entry, ok := decoded.(map[string]any)
			if !ok {
				return nil, errInvalidResponse
			}
			id, _ := entry["id"].(string)
			rawVersion, exists := entry["version"]
			if !exists && id == baseID {
				continue
			}
			version, _ := json.Marshal(rawVersion)
			number, err := templateVersionNumber(version)
			if err != nil {
				return nil, err
			}
			if number == 0 {
				if id != baseID {
					return nil, errInvalidResponse
				}
				continue
			}
			if id == baseID {
				return nil, errInvalidResponse
			}
			out[id] = entry
		}
	}
	return out, nil
}
