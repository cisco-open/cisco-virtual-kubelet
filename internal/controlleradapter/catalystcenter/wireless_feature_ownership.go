// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const featureCreatePrefix = "feature-create:v1:"
const featureCreatedPrefix = "feature-created:v1:"

func featureIdentityDigest(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func featureCreateSelected(resource string) bool {
	return strings.HasPrefix(resource, featureCreatePrefix) || featureRecreateSelected(resource)
}
func featureCreateKey(resource string) (string, bool) {
	if !featureCreateSelected(resource) {
		return "", false
	}
	canonical := featureCreateCanonical(resource)
	parts := strings.Split(strings.TrimPrefix(canonical, featureCreatePrefix), ":")
	if len(parts) != 3 || parts[0] != "ca" && parts[0] != "fra" {
		return "", false
	}
	for _, part := range parts[1:] {
		if len(part) != 64 || strings.ToLower(part) != part {
			return "", false
		}
		if _, err := hex.DecodeString(part); err != nil {
			return "", false
		}
	}
	return parts[0] + ":" + parts[1], true
}
func validFeatureCreateOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	_, ok := featureCreateKey(op.Resource)
	return ok && op.Section == "feature_templates" && op.Action == "Create" && op.TargetID == "" && op.ParentID == "" && op.TargetSerial == "" && op.CreatedResourceID == "" && op.PhysicalGroup == nil && op.DependencyVersions == nil && op.CancellationRequestedAt == nil && op.PreparedAt == nil && len(op.CredentialVersions) == 0 && len(op.SSIDCredentialHistory) == 0
}
func featureCreatedTarget(r cvk.NetworkControllerConfigurationActionApplication) (string, error) {
	op := &r.Operation
	key, ok := featureCreateKey(op.Resource)
	id, _, identityErr := featureReceiptIdentity(r.Resource, featureCreatedPrefix, key)
	if !ok || !validFeatureCreateOperation(op) || r.Section != op.Section || op.Phase != "Verified" || !configurationOperationID.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) || identityErr != nil {
		return "", errConfigurationOwned
	}
	return id, nil
}
func featureCreatedRecords(state *cvk.NetworkControllerConfigurationStatus) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	records := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	if state == nil {
		return records, nil
	}
	for _, r := range state.ActionApplications {
		if featureRetiredSelected(r.Resource) {
			continue
		}
		if !strings.HasPrefix(r.Resource, featureCreatedPrefix) && !featureCreateSelected(r.Operation.Resource) {
			continue
		}
		if _, err := featureCreatedTarget(r); err != nil {
			return nil, err
		}
		key, _ := featureCreateKey(r.Operation.Resource)
		if _, exists := records[key]; exists {
			return nil, errConfigurationOwned
		}
		records[key] = r
	}
	return records, nil
}
func featureCreateCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if !validFeatureCreateOperation(op) {
		return errConfigurationTransition
	}
	if _, err := featureRetiredCohorts(state); err != nil {
		return err
	}
	records, err := featureCreatedRecords(state)
	if err != nil {
		return err
	}
	key, _ := featureCreateKey(op.Resource)
	if featureRecreateSelected(op.Resource) {
		if err := featureRecreateAuthority(state, op); err != nil {
			return err
		}
	} else if _, exists := records[key]; exists {
		return errConfigurationOwned
	}
	if state != nil && len(state.ActionApplications) >= 256 {
		return errConfigurationBusy
	}
	return nil
}
func featureCreatedReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if !validFeatureCreateOperation(op) || op.Phase != "Verified" {
		return false
	}
	if _, err := featureRetiredCohorts(state); err != nil {
		return false
	}
	records, err := featureCreatedRecords(state)
	if err != nil {
		return false
	}
	key, _ := featureCreateKey(op.Resource)
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

type featureCreateBinding struct {
	kind, name, legacy, resource string
	verify                       func(context.Context, wirelessFeatureTaskReference) (string, bool, error)
	verifyState                  func(context.Context, wirelessFeatureTaskReference) (string, string, bool, error)
}

func (b featureCreateBinding) key() string { key, _ := featureCreateKey(b.resource); return key }

type featureOwnedCreateExecutor struct {
	base                                   configurationExecutor
	client                                 *client
	owner, ownerName                       string
	bindings                               map[string]featureCreateBinding
	receipts                               []cvk.NetworkControllerConfigurationActionApplication
	configGeneration, controllerGeneration int64
	sourceUID, sourceVersion               string
}

func (e *featureOwnedCreateExecutor) count() int { return e.base.count() }
func (e *featureOwnedCreateExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op != nil && (featureCreateSelected(op.Resource) || featureUpdateSelected(op.Resource)) {
		_, update := featureUpdateKey(op)
		if (!validFeatureCreateOperation(op) && !update) || op.ConfigUID != e.owner || op.ConfigName != e.ownerName || op.SubmittedAt == nil || !validAPIID(op.TaskID) {
			return false, errConfigurationTransition
		}
		for _, binding := range e.bindings {
			if binding.resource == featureCreateCanonical(op.Resource) || update && featureUpdatePrefix+strings.TrimPrefix(binding.resource, featureCreatePrefix) == op.Resource {
				id, done, err := binding.verify(ctx, wirelessFeatureTaskReference{ID: op.TaskID, Name: binding.name, Action: op.Action, Target: op.TargetID, Submitted: op.SubmittedAt.Time})
				if err == nil && done && featureRecreateSelected(op.Resource) {
					key, _ := featureCreateKey(op.Resource)
					err = featureRecreatedIDUnused(&cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}, key, id)
					if err != nil {
						return false, err
					}
				}
				return done, err
			}
		}
		return false, errConfigurationTransition
	}
	return e.base.verify(ctx, op)
}
func (e *featureOwnedCreateExecutor) next(ctx context.Context) (*configurationWrite, error) {
	state := &cvk.NetworkControllerConfigurationStatus{ActionApplications: e.receipts}
	records, err := featureCreatedRecords(state)
	if err != nil {
		return nil, err
	}
	if _, err = featureAppliedRecords(state); err != nil {
		return nil, err
	}
	if _, err = featureRetiredCohorts(state); err != nil {
		return nil, err
	}
	deleted, err := featureDeletedRecords(state)
	if err != nil {
		return nil, err
	}
	inventory := map[string]map[string]wirelessFeatureInstance{}
	missing := 0
	for _, b := range e.bindings {
		rows, exists := inventory[b.kind]
		if !exists {
			rows, err = e.client.wirelessFeatureInstances(ctx, b.kind)
			if err != nil {
				return nil, err
			}
			inventory[b.kind] = rows
		}
		actual, present := rows[b.name]
		if receipt, owned := records[b.key()]; owned {
			if _, wasDeleted := deleted[b.key()]; wasDeleted {
				if present {
					return nil, errConfigurationOwned
				}
				if err := e.recreationAbsent(ctx, b); err != nil {
					return nil, err
				}
			} else {
				id, err := featureCreatedTarget(receipt)
				if err != nil || receipt.Operation.ConfigUID != e.owner || receipt.Operation.ConfigName != e.ownerName || !present || actual.ID != id || *actual.System {
					return nil, errConfigurationOwned
				}
			}
		}
		if !present {
			missing++
		}
	}
	// Reserve room for the whole declared missing set before the first effect.
	if len(e.receipts)+missing > 256 {
		return nil, errConfigurationBusy
	}
	write, err := e.base.next(ctx)
	if err != nil || write == nil || (write.action != "Create" && write.action != "Update") {
		return write, err
	}
	binding, ok := e.bindings[write.resource]
	if !ok {
		return nil, errConfigurationTransition
	}
	copy := *write
	copy.resource = binding.resource
	if write.action == "Create" {
		copy.resource = featureRecreateIntent(binding, state)
		if featureRecreateSelected(copy.resource) {
			if err := featureRecreateAuthority(state, e.recreateProbe(copy.resource)); err != nil {
				return nil, err
			}
		}
	}
	if write.action == "Update" {
		if _, owned := records[binding.key()]; !owned {
			return write, nil
		}
		copy.resource = featureUpdatePrefix + strings.TrimPrefix(binding.resource, featureCreatePrefix)
		probe := &cvk.NetworkControllerConfigurationOperation{Section: "feature_templates", Resource: copy.resource, Action: "Update", TargetID: write.identity.target, ConfigUID: e.owner, ConfigName: e.ownerName}
		if err := featureUpdateCapacity(state, probe); err != nil {
			return nil, err
		}
	}
	return &copy, nil
}
