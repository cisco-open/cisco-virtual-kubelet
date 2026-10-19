// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Exclude only this operation's port. Every other assignment, including matching
// pre-existing desired ports, must retain its original native identity/settings.
func fabricPortAssignmentResource(deps *fabricPortAssignmentDependencies, port string) string {
	baseline := []fabricPortAssignment{}
	for _, row := range deps.before {
		if row.Interface != port {
			baseline = append(baseline, row)
		}
	}
	raw, _ := json.Marshal([]any{deps.prerequisites, port, baseline})
	return fmt.Sprintf("fabric-port-assignment-create:%x", sha256.Sum256(raw))
}

// Private primitive: the coordinator must retain the full group journal and
// obtain a one-use submission permit before providing the guard.
func (c *client) planGuardedFabricPortAssignmentCreate(ctx context.Context, want fabricPortAssignmentDeploymentIntent, port string, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	deps, err := c.fabricPortAssignmentDependencies(ctx, want)
	if err != nil {
		return nil, err
	}
	for _, row := range deps.selection.existing {
		if row.Interface == port {
			return nil, nil
		}
	}
	var selected *fabricPortAssignmentIntent
	for _, row := range deps.selection.missing {
		if *row.Interface == port {
			copy := row
			selected = &copy
		}
	}
	if selected == nil {
		return nil, errConfigurationTransition
	}
	body, err := fabricPortAssignmentPayload(deps.deviceID, deps.fabricID, []fabricPortAssignmentIntent{*selected}, deps.ports)
	if err != nil {
		return nil, err
	}
	var used atomic.Bool
	return &configurationWrite{resource: fabricPortAssignmentResource(deps, port), action: "Create", identity: configurationResourceIdentity{target: deps.deviceID, parent: deps.fabricID}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		fresh, err := c.fabricPortAssignmentDependencies(ctx, want)
		if err != nil {
			return Task{}, err
		}
		if fresh.digest != deps.digest {
			return Task{}, errConfigurationStale
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		raw, err := c.mutateQueryStatus(ctx, http.MethodPost, fabricPortAssignmentsPath, nil, body, http.StatusOK)
		if err != nil {
			return Task{}, err
		}
		var response struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &response) != nil || !validAPIID(response.Response.ID) {
			return Task{}, errInvalidResponse
		}
		return Task{ID: response.Response.ID}, nil
	}}, nil
}

// Caller verifies the owned task separately. No receipt or completion is
// produced here; absence remains pending, and unrelated changes reject recovery.
func (c *client) verifyFabricPortAssignmentCreate(ctx context.Context, want fabricPortAssignmentDeploymentIntent, port string, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
	if op == nil || op.Phase != "Verifying" || op.Action != "Create" || !validAPIID(op.TaskID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return "", errConfigurationTransition
	}
	deps, err := c.fabricPortAssignmentDependencies(ctx, want)
	if err != nil {
		return "", err
	}
	if op.Resource != fabricPortAssignmentResource(deps, port) || op.TargetID != deps.deviceID || op.ParentID != deps.fabricID {
		return "", errConfigurationStale
	}
	for _, row := range deps.selection.existing {
		if row.Interface == port {
			return row.ID, nil
		}
	}
	for _, row := range deps.selection.missing {
		if *row.Interface == port {
			return "", nil
		}
	}
	return "", errConfigurationTransition
}
