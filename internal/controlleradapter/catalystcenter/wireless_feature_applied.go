// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"reflect"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const featureUpdatePrefix = "feature-update:v1:"
const featureAppliedPrefix = "feature-applied:v1:"

func featureUpdateSelected(resource string) bool {
	return strings.HasPrefix(resource, featureUpdatePrefix)
}
func featureUpdateKey(op *cvk.NetworkControllerConfigurationOperation) (string, bool) {
	if op == nil || !featureUpdateSelected(op.Resource) || op.Action != "Update" || !canonicalWirelessFeatureUUID(op.TargetID) {
		return "", false
	}
	copy := *op
	copy.Resource = featureCreatePrefix + strings.TrimPrefix(op.Resource, featureUpdatePrefix)
	copy.Action, copy.TargetID = "Create", ""
	if !validFeatureCreateOperation(&copy) {
		return "", false
	}
	return featureCreateKey(copy.Resource)
}

// Applied state is a separate, replaceable observation. It never overwrites the
// immutable creator and cannot establish ownership of an imported definition.
func featureAppliedRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	result := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	creators, err := featureCreatedRecords(state)
	if err != nil {
		return nil, err
	}
	if state == nil {
		return result, nil
	}
	for _, r := range state.ActionApplications {
		if featureRetiredSelected(r.Resource) {
			continue
		}
		if !strings.HasPrefix(r.Resource, featureAppliedPrefix) && !featureUpdateSelected(r.Operation.Resource) {
			continue
		}
		op := &r.Operation
		key, ok := featureUpdateKey(op)
		creator, exists := creators[key]
		id, creatorErr := featureCreatedTarget(creator)
		observedID, _, identityErr := featureReceiptIdentity(r.Resource, featureAppliedPrefix, key)
		if !ok || !exists || creatorErr != nil || id != op.TargetID || identityErr != nil || observedID != id || r.Section != op.Section || op.Phase != "Verified" || !configurationOperationID.MatchString(op.ID) || op.ConfigUID != creator.Operation.ConfigUID || op.ConfigName != creator.Operation.ConfigName || op.ConfigGeneration < creator.Operation.ConfigGeneration || op.ControllerGeneration < creator.Operation.ControllerGeneration || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.Before(creator.VerifiedAt) || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) {
			return nil, errConfigurationOwned
		}
		if _, duplicate := result[key]; duplicate {
			return nil, errConfigurationOwned
		}
		result[key] = r
	}
	return result, nil
}
func featureUpdateCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if _, err := featureRetiredCohorts(state); err != nil {
		return err
	}
	key, ok := featureUpdateKey(op)
	if !ok {
		return errConfigurationTransition
	}
	creators, err := featureCreatedRecords(state)
	if err != nil {
		return err
	}
	creator, exists := creators[key]
	id, err := featureCreatedTarget(creator)
	if !exists || err != nil || id != op.TargetID || creator.Operation.ConfigUID != op.ConfigUID || creator.Operation.ConfigName != op.ConfigName {
		return errConfigurationOwned
	}
	applied, err := featureAppliedRecords(state)
	if err != nil {
		return err
	}
	if _, exists := applied[key]; !exists && len(state.ActionApplications) >= 256 {
		return errConfigurationBusy
	}
	return nil
}
func featureAppliedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if _, err := featureRetiredCohorts(state); err != nil {
		return false
	}
	key, ok := featureUpdateKey(op)
	if !ok || op.Phase != "Verified" {
		return false
	}
	records, err := featureAppliedRecords(state)
	if err != nil {
		return false
	}
	r, exists := records[key]
	if !exists || !reflect.DeepEqual(r.Operation, *op) {
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
