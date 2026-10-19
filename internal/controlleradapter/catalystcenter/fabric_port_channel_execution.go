// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"sync/atomic"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func fabricPortChannelResource(deps *fabricPortChannelDependencies) string {
	return "fabric-port-channel-create:" + deps.recoveryDigest
}

// Private native primitive. The coordinator must journal the complete physical
// footprint and obtain a one-use group permit before supplying a dispatch guard.
// Native acceptance alone does not establish completion or resource ownership.
func (c *client) planGuardedFabricPortChannelCreate(ctx context.Context, want fabricPortChannelDeploymentIntent, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	// 2.3.7.6 requires protocol on creation. The model permits omission, so do
	// not invent a default: existing matching channels can still be observed.
	deps, err := c.fabricPortChannelDependencies(ctx, want)
	if err != nil {
		return nil, err
	}
	if deps.existing != nil {
		return nil, nil
	}
	if want.channel.Protocol == nil {
		return nil, errConfigurationTransition
	}
	body, err := fabricPortChannelPayload(deps.deviceID, deps.fabricID, want.channel, deps.ports)
	if err != nil {
		return nil, err
	}
	var used atomic.Bool
	return &configurationWrite{resource: fabricPortChannelResource(deps), action: "Create", identity: configurationResourceIdentity{target: deps.deviceID, parent: deps.fabricID}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		fresh, err := c.fabricPortChannelDependencies(ctx, want)
		if err != nil {
			return Task{}, err
		}
		if fresh.digest != deps.digest {
			return Task{}, errConfigurationStale
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		raw, err := c.mutateQueryStatus(ctx, http.MethodPost, fabricPortChannelsPath, nil, body, http.StatusOK)
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

// Readback after independent task verification. Returns the native UUID only
// when prerequisites and all unrelated channels still match the journal.
func (c *client) verifyFabricPortChannelCreate(ctx context.Context, want fabricPortChannelDeploymentIntent, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
	if op == nil || op.Phase != "Verifying" || op.Action != "Create" || !validAPIID(op.TaskID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return "", errConfigurationTransition
	}
	deps, err := c.fabricPortChannelDependencies(ctx, want)
	if err != nil {
		return "", err
	}
	if op.Resource != fabricPortChannelResource(deps) || op.TargetID != deps.deviceID || op.ParentID != deps.fabricID {
		return "", errConfigurationStale
	}
	if deps.existing == nil {
		return "", nil
	}
	return deps.existing.ID, nil
}
