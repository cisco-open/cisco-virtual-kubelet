// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/hex"
	"encoding/json"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"reflect"
	"strings"
	"time"
)

const tagOwnedPrefix = "tag-owned:v1:"
const tagCreatedPrefix = "tag-created:v1:"
const tagAppliedPrefix = "tag-applied:v1:"

func tagOwnedSelected(resource string) bool {
	return strings.HasPrefix(resource, tagOwnedPrefix) || tagRecreateSelected(resource) || tagSuccessorSelected(resource)
}
func tagOwnedParts(op *cvk.NetworkControllerConfigurationOperation) (key string, create bool, ok bool) {
	if op == nil || !tagOwnedSelected(op.Resource) {
		return
	}
	parts := strings.Split(strings.TrimPrefix(tagOwnedCanonical(op.Resource), tagOwnedPrefix), ":")
	if len(parts) != 4 || parts[0] != "c" && parts[0] != "u" || parts[3] != "i" && parts[3] != "c" {
		return
	}
	for _, digest := range parts[1:3] {
		raw, err := hex.DecodeString(digest)
		if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != digest {
			return
		}
	}
	if op.Section != "inventory" && op.Section != "templates" || op.ParentID != "" || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil || op.PreparedAt != nil || len(op.CredentialVersions) != 0 || len(op.SSIDCredentialHistory) != 0 {
		return
	}
	// Source mode prevents silent loss of a projected ConfigMap binding.
	if parts[3] == "c" && (op.Phase != "" || op.ConfigUID != "" || op.SourceUID != "" || op.SourceVersion != "") && (op.SourceUID == "" || op.SourceVersion == "") || parts[3] == "i" && (op.SourceUID != "" || op.SourceVersion != "") {
		return
	}
	create = parts[0] == "c"
	if create && (op.Action != "Create" || op.TargetID != "") || !create && (op.Action != "Update" || !canonicalTemplateUUID(op.TargetID)) {
		return "", false, false
	}
	return parts[1], create, true
}
func validTagOwnedOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	_, _, ok := tagOwnedParts(op)
	return ok
}

type tagOwnershipRecords struct {
	created, applied map[string]cvk.NetworkControllerConfigurationActionApplication
}

func tagReceiptIdentity(r cvk.NetworkControllerConfigurationActionApplication) (string, string, string, error) {
	key, create, ok := tagOwnedParts(&r.Operation)
	op := &r.Operation
	prefix := tagAppliedPrefix
	if create {
		prefix = tagCreatedPrefix
	}
	parts := strings.Split(strings.TrimPrefix(r.Resource, prefix), ":")
	if !ok || !strings.HasPrefix(r.Resource, prefix) || (len(parts) != 3 && len(parts) != 5) || len(parts) == 5 && (parts[3] != "s" || !validTemplateComparisonDigest(parts[4])) || parts[0] != key || !canonicalTemplateUUID(parts[1]) || !validTemplateComparisonDigest(parts[2]) || r.Section != op.Section || op.Phase != "Verified" || !configurationOperationID.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) || !create && parts[1] != op.TargetID {
		return "", "", "", errConfigurationOwned
	}
	return key, parts[1], parts[2], nil
}
func tagOwnedRecords(state *cvk.NetworkControllerConfigurationStatus) (tagOwnershipRecords, error) {
	out := tagOwnershipRecords{created: map[string]cvk.NetworkControllerConfigurationActionApplication{}, applied: map[string]cvk.NetworkControllerConfigurationActionApplication{}}
	if state == nil {
		return out, nil
	}
	ids, ops := map[string]bool{}, map[string]bool{}
	for _, r := range state.ActionApplications {
		if tagRetiredSelected(r.Resource) || tagOwnerArchivedSelected(r.Resource) {
			continue
		}
		if !tagOwnedSelected(r.Operation.Resource) && !strings.HasPrefix(r.Resource, tagCreatedPrefix) && !strings.HasPrefix(r.Resource, tagAppliedPrefix) {
			continue
		}
		key, id, _, err := tagReceiptIdentity(r)
		if err != nil {
			return out, err
		}
		_, create, _ := tagOwnedParts(&r.Operation)
		target := out.applied
		if create {
			target = out.created
			if ids[id] {
				return out, errConfigurationOwned
			}
			ids[id] = true
		}
		if _, exists := target[key]; exists || ops[r.Operation.ID] {
			return out, errConfigurationOwned
		}
		target[key] = r
		ops[r.Operation.ID] = true
	}
	for key, r := range out.applied {
		creator, exists := out.created[key]
		_, id, _, err := tagReceiptIdentity(creator)
		if !exists || err != nil || id != r.Operation.TargetID || r.Section != creator.Section || r.Operation.ConfigUID != creator.Operation.ConfigUID || r.Operation.ConfigName != creator.Operation.ConfigName || r.Operation.ConfigGeneration < creator.Operation.ConfigGeneration || r.Operation.ControllerGeneration < creator.Operation.ControllerGeneration || r.Operation.SubmittedAt.Before(creator.VerifiedAt) {
			return out, errConfigurationOwned
		}
	}
	return out, nil
}
func tagOwnedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if _, err := tagRetiredCohorts(state); err != nil {
		return false
	}
	key, create, ok := tagOwnedParts(op)
	if !ok || op.Phase != "Verified" {
		return false
	}
	records, err := tagOwnedRecords(state)
	if err != nil {
		return false
	}
	r := records.applied[key]
	if create {
		r = records.created[key]
	}
	if !reflect.DeepEqual(r.Operation, *op) {
		return false
	}
	count := 0
	for _, entry := range state.ActionApplications {
		if entry.Operation.ID == op.ID {
			count++
		}
	}
	return count == 1
}
func tagOwnedCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if op != nil && op.Action == "Create" {
		if err := tagSuccessorAuthority(state, op); err != nil {
			return err
		}
	}
	if _, err := tagRetiredCohorts(state); err != nil {
		return err
	}
	key, create, ok := tagOwnedParts(op)
	if !ok {
		return errConfigurationTransition
	}
	records, err := tagOwnedRecords(state)
	if err != nil {
		return err
	}
	creator, exists := records.created[key]
	if tagRecreateSelected(op.Resource) {
		if err := tagRecreateAuthority(state, op); err != nil {
			return err
		}
	} else if create && exists {
		return errConfigurationOwned
	}
	if !create {
		_, id, _, err := tagReceiptIdentity(creator)
		if !exists || err != nil || id != op.TargetID || creator.Section != op.Section || creator.Operation.ConfigUID != op.ConfigUID || creator.Operation.ConfigName != op.ConfigName {
			return errConfigurationOwned
		}
	}
	count := 0
	if state != nil {
		count = len(state.ActionApplications)
	}
	_, already := records.applied[key]
	if (create || !already) && count >= 256 {
		return errConfigurationBusy
	}
	// The resource protocol bounds each retained row; also preserve the shared
	// byte ceiling before dispatch, including the replacement Update row.
	raw, err := json.Marshal(state)
	if err != nil || len(raw) > 250<<10 {
		return errConfigurationBusy
	}
	return nil
}
