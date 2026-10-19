// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const templateApplyPrefix = "template-apply:v1:"
const templateAppliedPrefix = "template-applied:v1:"

// Only new, privately baselined, task-owned objects enter this protocol. The
// final stage distinguishes a draft write from a version commit on the same UUID.
func templateApplyResource(creator, stage string) string {
	if templateCreatorComparisonKey(creator) == "" {
		return ""
	}
	return templateApplyPrefix + strings.TrimPrefix(creator, templateCreatePrefix) + ":" + stage
}
func templateApplyIdentity(op *cvk.NetworkControllerConfigurationOperation) (string, string, string, bool) {
	if op == nil || !strings.HasPrefix(op.Resource, templateApplyPrefix) || op.Action != "Update" || !canonicalTemplateUUID(op.TargetID) {
		return "", "", "", false
	}
	parts := strings.Split(strings.TrimPrefix(op.Resource, templateApplyPrefix), ":")
	if len(parts) != 6 && (len(parts) != 7 || parts[6] != templateVersionOwnershipTag || parts[5] != "commit") {
		return "", "", "", false
	}
	creator := templateCreatePrefix + strings.Join(parts[:5], ":")
	key, ok := templateCreateKey(creator)
	stage := parts[5]
	if !ok || templateCreatorComparisonKey(creator) == "" || parts[0] == "p" && stage != "project" || parts[0] != "p" && stage != "draft" && stage != "commit" {
		return "", "", "", false
	}
	copy := op.DeepCopy()
	copy.Resource, copy.Action, copy.TargetID = creator, "Create", ""
	if !validTemplateCreateOperation(copy) {
		return "", "", "", false
	}
	return key, creator, stage, true
}
func templateCreatorReceiptDigest(r cvk.NetworkControllerConfigurationActionApplication) string {
	// The record contains only public identity and keyed fingerprints, never body.
	raw, _ := json.Marshal(r)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func templateAppliedCurrentRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	out := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	creators, err := templateCreatedRecords(state)
	if err != nil || state == nil {
		return out, err
	}
	for _, r := range state.ActionApplications {
		if strings.HasPrefix(r.Resource, templateAppliedHistoryPrefix) {
			continue
		}
		if !strings.HasPrefix(r.Resource, templateAppliedPrefix) && !strings.HasPrefix(r.Operation.Resource, templateApplyPrefix) {
			continue
		}
		op := &r.Operation
		key, resource, _, ok := templateApplyIdentity(op)
		original, exists := creators[key]
		id, originalErr := templateCreatedTarget(original)
		keyID := templateCreatorComparisonKey(resource)
		prefix := templateAppliedPrefix + key + ":" + id + ":" + templateComparisonTag + ":" + keyID + ":"
		tail := strings.Split(strings.TrimPrefix(r.Resource, prefix), ":")
		if !ok || !exists || originalErr != nil || id != op.TargetID || keyID != templateCreatorComparisonKey(original.Operation.Resource) || op.ParentID != original.Operation.ParentID || op.ConfigUID != original.Operation.ConfigUID || op.ConfigName != original.Operation.ConfigName || op.ConfigGeneration < original.Operation.ConfigGeneration || op.ControllerGeneration < original.Operation.ControllerGeneration || !strings.HasPrefix(r.Resource, prefix) || !validTemplateAppliedTail(op, tail) || !validTemplateComparisonDigest(tail[0]) || tail[1] != templateCreatorReceiptDigest(original) || op.Phase != "Verified" || r.Section != "templates" || !configurationOperationID.MatchString(op.ID) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.Before(original.VerifiedAt) || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) {
			return nil, errConfigurationOwned
		}
		if _, duplicate := out[key]; duplicate {
			return nil, errConfigurationOwned
		}
		out[key] = r
	}
	return out, nil
}
func templateApplyCreator(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) (cvk.NetworkControllerConfigurationActionApplication, error) {
	var zero cvk.NetworkControllerConfigurationActionApplication
	key, resource, _, ok := templateApplyIdentity(op)
	if !ok {
		return zero, errConfigurationTransition
	}
	creators, err := templateCreatedRecords(state)
	if err != nil {
		return zero, err
	}
	r, exists := creators[key]
	id, err := templateCreatedTarget(r)
	if !exists || err != nil || id != op.TargetID || r.Operation.ParentID != op.ParentID || r.Operation.ConfigUID != op.ConfigUID || r.Operation.ConfigName != op.ConfigName || r.Operation.ConfigGeneration > op.ConfigGeneration || r.Operation.ControllerGeneration > op.ControllerGeneration || templateCreatorComparisonKey(r.Operation.Resource) != templateCreatorComparisonKey(resource) {
		return zero, errConfigurationOwned
	}
	return r, nil
}
func templateAppliedResult(op *cvk.NetworkControllerConfigurationOperation, original cvk.NetworkControllerConfigurationActionApplication, mac string) string {
	key, resource, _, ok := templateApplyIdentity(op)
	if !ok || !validTemplateComparisonDigest(mac) {
		return ""
	}
	return templateAppliedPrefix + key + ":" + op.TargetID + ":" + templateComparisonTag + ":" + templateCreatorComparisonKey(resource) + ":" + mac + ":" + templateCreatorReceiptDigest(original)
}
func templateReplaceApplied(state *cvk.NetworkControllerConfigurationStatus, row cvk.NetworkControllerConfigurationActionApplication) error {
	key, _, _, ok := templateApplyIdentity(&row.Operation)
	if !ok {
		return errConfigurationTransition
	}
	if _, err := templateAppliedRecords(state); err != nil {
		return err
	}
	if _, err := templateApplyCreator(state, &row.Operation); err != nil {
		return err
	}
	kept := make([]cvk.NetworkControllerConfigurationActionApplication, 0, len(state.ActionApplications)+1)
	for _, r := range state.ActionApplications {
		other, _, _, selected := templateApplyIdentity(&r.Operation)
		if strings.HasPrefix(r.Resource, templateAppliedHistoryPrefix) || !selected || other != key {
			kept = append(kept, r)
		} else if templateVersionOperation(&r.Operation) {
			archived := *r.DeepCopy()
			archived.Resource = templateAppliedHistoryPrefix + strings.TrimPrefix(r.Resource, templateAppliedPrefix)
			kept = append(kept, archived)
		}
	}
	kept = append(kept, row)
	raw, err := json.Marshal(kept)
	if err != nil || len(kept) > 256 || len(raw) > 256<<10 {
		return errConfigurationBusy
	}
	candidate := &cvk.NetworkControllerConfigurationStatus{ActionApplications: kept}
	if _, err := templateAppliedRecords(candidate); err != nil {
		return err
	}
	state.ActionApplications = kept
	return nil
}
func templateApplyCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	original, err := templateApplyCreator(state, op)
	if err != nil {
		return err
	}
	copy := *state.DeepCopy()
	future := op.DeepCopy()
	future.Phase = "Verified"
	future.TaskID = strings.Repeat("x", 128)
	now := metav1.Now()
	future.SubmittedAt = &now
	// Capacity is checked before dispatch, without inventing an actual receipt.
	result := templateAppliedResult(future, original, strings.Repeat("a", 64))
	if templateVersionOperation(future) {
		result = templateVersionCapacityPlaceholder(state, future, original)
	}
	row := cvk.NetworkControllerConfigurationActionApplication{Section: "templates", Resource: result, Operation: *future, VerifiedAt: &now}
	return templateReplaceApplied(&copy, row)
}
func templateAppliedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	key, _, _, ok := templateApplyIdentity(op)
	if !ok || op.Phase != "Verified" {
		return false
	}
	records, err := templateAppliedRecords(state)
	if err != nil {
		return false
	}
	r, exists := records[key]
	if !exists || !reflect.DeepEqual(r.Operation, *op) {
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
