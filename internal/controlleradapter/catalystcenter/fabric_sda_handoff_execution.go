// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"net/http"
	"reflect"
	"sync/atomic"
)

func fabricSDAHandoffMatches(want, got fabricSDAHandoff) bool {
	return reflect.DeepEqual(want, got)
}
func fabricSDAHandoffResource(deps *fabricSDAHandoffDependencies) string {
	return "fabric-sda-handoff-create:" + deps.recoveryDigest
}

// Private dependency-bound create primitive. The adapter must still persist
// ownership/journal state and obtain physical admission before invoking submit. A write object can be used only once,
// including when its outcome is unknown; this is not an automatic retry path.
func (c *client) planGuardedFabricSDAHandoffCreate(ctx context.Context, want fabricSDAHandoffDeploymentIntent, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	deps, err := c.fabricSDAHandoffDependencies(ctx, want)
	if err != nil {
		return nil, err
	}
	desired := deps.binding.desired
	for _, row := range deps.before {
		if fabricSDAHandoffMatches(desired, row) {
			return nil, nil
		}
		return nil, errConfigurationStale
	}
	body, err := json.Marshal([]fabricSDAHandoff{desired})
	if err != nil {
		return nil, errInvalidResponse
	}
	var used atomic.Bool
	return &configurationWrite{resource: fabricSDAHandoffResource(deps), action: "Create", identity: configurationResourceIdentity{target: desired.DeviceID, parent: desired.FabricID}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		fresh, err := c.fabricSDAHandoffDependencies(ctx, want)
		if err != nil {
			return Task{}, err
		}
		if fresh.digest != deps.digest {
			return Task{}, errConfigurationStale
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		raw, err := c.mutateQueryStatus(ctx, http.MethodPost, fabricSDAHandoffsPath, nil, body, http.StatusOK)
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

// Native recovery evidence only: the coordinator must independently verify the
// recorded task and group authority before publishing any ownership receipt.
func (c *client) verifyFabricSDAHandoffCreate(ctx context.Context, want fabricSDAHandoffDeploymentIntent, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Phase != "Verifying" || op.Action != "Create" || !validAPIID(op.TaskID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	deps, err := c.fabricSDAHandoffDependencies(ctx, want)
	if err != nil {
		return false, err
	}
	if op.Resource != fabricSDAHandoffResource(deps) || op.TargetID != deps.binding.desired.DeviceID || op.ParentID != deps.binding.desired.FabricID {
		return false, errConfigurationStale
	}
	for _, row := range deps.before {
		if fabricSDAHandoffMatches(deps.binding.desired, row) {
			return true, nil
		}
	}
	return false, nil
}
