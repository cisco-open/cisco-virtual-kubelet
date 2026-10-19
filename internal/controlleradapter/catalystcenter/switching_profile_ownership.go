// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/google/uuid"
)

// # cannot occur in a literal profile name, so no legacy name is reinterpreted.
const switchingCreateLocal = "#created:v1:"
const switchingCreatePrefix = "profiles/" + switchingCreateLocal
const switchingCreatedPrefix = "switching-created:v1:"

func switchingNameDigest(name string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(name))) }
func switchingCreateSelected(resource string) bool {
	return strings.HasPrefix(resource, switchingCreatePrefix)
}
func switchingCreateKey(resource string) (string, bool) {
	if !switchingCreateSelected(resource) {
		return "", false
	}
	key := strings.TrimPrefix(resource, switchingCreatePrefix)
	return key, validTemplateComparisonDigest(key)
}
func validSwitchingCreateOperation(op *cvk.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	_, ok := switchingCreateKey(op.Resource)
	return ok && op.Section == "network_profiles" && op.Action == "Create" && op.TargetID == "" && validSwitchingProfileBinding(op.ParentID) && op.TargetSerial == "" && op.CreatedResourceID == "" && op.PhysicalGroup == nil && op.DependencyVersions == nil && op.PreparedAt == nil && op.CancellationRequestedAt == nil && len(op.CredentialVersions) == 0 && len(op.SSIDCredentialHistory) == 0
}
func switchingCreatedKey(op *cvk.NetworkControllerConfigurationOperation, id string) string {
	key, ok := switchingCreateKey(op.Resource)
	if !ok {
		return ""
	}
	return switchingCreatedPrefix + key + ":" + id
}
func switchingCreatedTarget(r cvk.NetworkControllerConfigurationActionApplication) (string, error) {
	op := &r.Operation
	if !validSwitchingCreateOperation(op) || op.Phase != "Verified" || r.Section != op.Section || !configurationOperationID.MatchString(op.ID) || op.ConfigName == "" || op.ConfigUID == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || r.VerifiedAt == nil || r.VerifiedAt.IsZero() || r.VerifiedAt.Before(op.SubmittedAt) || r.VerifiedAt.After(time.Now()) || (op.SourceUID == "") != (op.SourceVersion == "") {
		return "", errConfigurationOwned
	}
	prefix := switchingCreatedKey(op, "")
	id, ok := strings.CutPrefix(r.Resource, prefix)
	parsed, err := uuid.Parse(id)
	if !ok || err != nil || parsed == uuid.Nil || parsed.String() != id {
		return "", errConfigurationOwned
	}
	return id, nil
}
func switchingCreatorRecords(state *cvk.NetworkControllerConfigurationStatus, owner string) (map[string]cvk.NetworkControllerConfigurationActionApplication, error) {
	out := map[string]cvk.NetworkControllerConfigurationActionApplication{}
	ids, operations, tasks := map[string]bool{}, map[string]bool{}, map[string]bool{}
	if state == nil {
		return out, nil
	}
	for _, r := range state.ActionApplications {
		if !strings.HasPrefix(r.Resource, switchingCreatedPrefix) && !switchingCreateSelected(r.Operation.Resource) {
			continue
		}
		id, err := switchingCreatedTarget(r)
		if err != nil || r.Operation.ConfigUID != owner || ids[id] || operations[r.Operation.ID] || tasks[r.Operation.TaskID] {
			return nil, errConfigurationOwned
		}
		owners := 0
		for _, o := range state.Owners {
			if o.Section == "network_profiles" {
				if o.ConfigUID != r.Operation.ConfigUID || o.ConfigName != r.Operation.ConfigName {
					return nil, errConfigurationOwned
				}
				owners++
			}
		}
		if owners != 1 {
			return nil, errConfigurationOwned
		}
		ids[id] = true
		operations[r.Operation.ID] = true
		tasks[r.Operation.TaskID] = true
		key, _ := switchingCreateKey(r.Operation.Resource)
		if _, ok := out[key]; ok {
			return nil, errConfigurationOwned
		}
		out[key] = r
	}
	return out, nil
}
func switchingCreatorCapacity(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) error {
	if state == nil || !validSwitchingCreateOperation(op) {
		return errConfigurationTransition
	}
	rows, err := switchingCreatorRecords(state, op.ConfigUID)
	if err != nil {
		return err
	}
	key, _ := switchingCreateKey(op.Resource)
	if r, ok := rows[key]; ok {
		expected := op.DeepCopy()
		expected.Phase = "Verified"
		if !reflect.DeepEqual(r.Operation, *expected) {
			return errConfigurationOwned
		}
		return nil
	}
	if len(state.ActionApplications) >= 256 {
		return errConfigurationBusy
	}
	return nil
}
func switchingCreatorReceiptPresent(state *cvk.NetworkControllerConfigurationStatus, op *cvk.NetworkControllerConfigurationOperation) bool {
	if state == nil || op == nil || op.Phase != "Verified" {
		return false
	}
	rows, err := switchingCreatorRecords(state, op.ConfigUID)
	if err != nil {
		return false
	}
	key, ok := switchingCreateKey(op.Resource)
	if !ok {
		return false
	}
	r, ok := rows[key]
	return ok && reflect.DeepEqual(r.Operation, *op)
}
func (e *networkProfileConfiguration) switchingCreatorSelection(op *cvk.NetworkControllerConfigurationOperation) (*switchingProfileExecutor, string, bool) {
	if !validSwitchingCreateOperation(op) {
		return nil, "", false
	}
	key, _ := switchingCreateKey(op.Resource)
	for _, part := range e.parts {
		if p, ok := part.executor.(*switchingProfileExecutor); ok && part.name == "profiles" {
			for _, want := range p.desired {
				if switchingNameDigest(want.name) == key {
					return &switchingProfileExecutor{client: p.client, desired: []switchingProfileIntent{want}}, want.name, true
				}
			}
		}
	}
	return nil, "", false
}

// One creator ledger and the existing owned metadata observation/atomic writer.
// Legacy in-flight Creates retain their original unowned convergence contract.
func (a *adapter) bindSwitchingCreatorOwnership(nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) error {
	if plan == nil || plan.section != "network_profiles" {
		return nil
	}
	e, ok := plan.executor.(*networkProfileConfiguration)
	if !ok {
		return nil
	}
	rows, err := switchingCreatorRecords(nc.Status.Configuration, string(config.UID))
	if err != nil {
		return err
	}
	legacy := false
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		op := nc.Status.Configuration.Operation
		legacy = op.Action == "Create" && strings.HasPrefix(op.Resource, "profiles/") && !switchingCreateSelected(op.Resource)
	}
	for _, part := range e.parts {
		if p, ok := part.executor.(*switchingProfileExecutor); ok && part.name == "profiles" {
			p.creators = rows
			p.creatorOwner = string(config.UID)
			p.createCustody = !legacy
			if nc.Status.Configuration != nil {
				p.receiptCount = len(nc.Status.Configuration.ActionApplications)
			}
			p.createAdmission = func(ctx context.Context, resource string, identity configurationResourceIdentity) error {
				return a.checkOwnedConfigurationDispatch(ctx, nc, config, source, "network_profiles", "profiles/"+resource, "Create", identity, validSwitchingCreateOperation, switchingCreatorCapacity)
			}
		}
	}
	prior := *plan
	plan.receiptOperation = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return op != nil && switchingCreateSelected(op.Resource) || prior.recordCompletion != nil && (prior.receiptOperation == nil || prior.receiptOperation(op))
	}
	plan.recordCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) error {
		if op == nil {
			return errConfigurationTransition
		}
		if !switchingCreateSelected(op.Resource) {
			if prior.recordCompletion != nil {
				return prior.recordCompletion(ctx, op)
			}
			return errConfigurationTransition
		}
		_, name, ok := e.switchingCreatorSelection(op)
		if !ok {
			return errConfigurationTransition
		}
		return a.recordOwnedInventoryAction(ctx, config, source, op.ID, func(ctx context.Context, current *cvk.NetworkControllerConfigurationOperation) (string, error) {
			var id string
			done, err := a.observeOwnedConfigurationOperation(ctx, config, source, current, "network_profiles", func(ctx context.Context) (bool, error) {
				local := current.DeepCopy()
				local.Resource = "profiles/" + name
				var done bool
				var err error
				id, done, err = e.verifySwitchingProfileCreatedResult(ctx, local, time.Now())
				return done, err
			})
			if err != nil || !done {
				return "", err
			}
			return switchingCreatedKey(current, id), nil
		})
	}
	return nil
}
