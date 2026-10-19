// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"sync/atomic"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func fabricL3IPHandoffMatches(want, got fabricL3IPHandoff) bool {
	got.ID = ""
	return want.ID == "" && reflect.DeepEqual(want, got)
}
func fabricL3IPHandoffResource(deps *fabricL3IPHandoffDependencies) string {
	return "fabric-l3-ip-handoff-create:" + deps.recoveryDigest
}

// Private dependency-bound create primitive. The adapter must still bind the
// persist ownership/journal state and obtain physical
// admission before invoking submit. A write object can be used only once,
// including when its outcome is unknown; this is not an automatic retry path.
func (c *client) planGuardedFabricL3IPHandoffCreate(ctx context.Context, want fabricL3IPHandoffIntent, device fabricDeviceIntent, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	deps, err := c.fabricL3IPHandoffDependencies(ctx, want, device)
	if err != nil {
		return nil, err
	}
	desired := deps.binding.desired
	for _, row := range deps.before {
		if fabricL3IPHandoffMatches(desired, row) {
			return nil, nil
		}
		if strings.EqualFold(row.Port, desired.Port) && (row.VLAN == desired.VLAN || row.TransitID == desired.TransitID && row.Network == desired.Network) {
			return nil, errConfigurationStale
		}
	}
	body, err := json.Marshal([]fabricL3IPHandoff{desired})
	if err != nil {
		return nil, errInvalidResponse
	}
	var used atomic.Bool
	return &configurationWrite{resource: fabricL3IPHandoffResource(deps), action: "Create", identity: configurationResourceIdentity{target: desired.DeviceID, parent: desired.FabricID}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		fresh, err := c.fabricL3IPHandoffDependencies(ctx, want, device)
		if err != nil {
			return Task{}, err
		}
		if fresh.digest != deps.digest {
			return Task{}, errConfigurationStale
		}
		if guard != nil {
			if err := guard(ctx); err != nil {
				return Task{}, err
			}
		}
		raw, err := c.mutateQueryStatus(ctx, http.MethodPost, fabricL3IPHandoffsPath, nil, body, http.StatusOK)
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

// Native readback after the coordinator has independently verified the recorded
// task. Return the native UUID for durable ownership; an absent row is pending.
// Changed dependency/peer evidence cannot be adopted as this operation's result.
func (c *client) verifyFabricL3IPHandoffCreate(ctx context.Context, want fabricL3IPHandoffIntent, device fabricDeviceIntent, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
	if op == nil || op.Phase != "Verifying" || op.Action != "Create" || !validAPIID(op.TaskID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return "", errConfigurationTransition
	}
	deps, err := c.fabricL3IPHandoffDependencies(ctx, want, device)
	if err != nil {
		return "", err
	}
	if op.Resource != fabricL3IPHandoffResource(deps) || op.TargetID != deps.binding.desired.DeviceID || op.ParentID != deps.binding.desired.FabricID {
		return "", errConfigurationStale
	}
	for _, row := range deps.before {
		if fabricL3IPHandoffMatches(deps.binding.desired, row) {
			return row.ID, nil
		}
	}
	return "", nil
}
