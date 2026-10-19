// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
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

// Exclude only the setting this operation changes. Recovery must retain the
// fabric identity, complete membership and VLAN/SSID/security-group baseline.
func fabricWirelessMulticastResource(path string, enabled bool, deps *fabricWirelessMulticastDependencies) string {
	evidence := []any{path, enabled, deps.state.siteID, deps.state.fabricID, deps.members, deps.mappings}
	if len(deps.embedded) > 0 {
		evidence = append(evidence, deps.embedded)
	}
	raw, _ := json.Marshal(evidence)
	return fmt.Sprintf("fabric-wireless-multicast-update:%x", sha256.Sum256(raw))
}

// Private until the coordinator journals the complete physical group and
// supplies a one-use submission permit through guard.
func (c *client) planGuardedFabricWirelessMulticast(ctx context.Context, path string, enabled bool, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	deps, err := c.wirelessMulticastDependencies(ctx, path)
	if err != nil {
		return nil, err
	}
	if deps.state.enabled == enabled {
		return nil, nil
	}
	endpoint, err := fabricWirelessMulticastPath(deps.state.fabricID)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(map[string]bool{"multicastEnabled": enabled})
	if err != nil {
		return nil, err
	}
	var used atomic.Bool
	return &configurationWrite{resource: fabricWirelessMulticastResource(path, enabled, deps), action: "Update", identity: configurationResourceIdentity{target: deps.state.fabricID, parent: deps.state.siteID}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		fresh, err := c.wirelessMulticastDependencies(ctx, path)
		if err != nil {
			return Task{}, err
		}
		if fresh.digest != deps.digest {
			return Task{}, errConfigurationStale
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		raw, err := c.mutateQueryStatus(ctx, http.MethodPut, endpoint, nil, body, http.StatusOK)
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

// Native convergence only. The caller must independently verify the journal's
// owned task, group authority and repeated physical target observations.
func (c *client) verifyFabricWirelessMulticast(ctx context.Context, path string, enabled bool, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Phase != "Verifying" || op.Action != "Update" || !validAPIID(op.TaskID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	deps, err := c.wirelessMulticastDependencies(ctx, path)
	if err != nil {
		return false, err
	}
	if op.TargetID != deps.state.fabricID || op.ParentID != deps.state.siteID || op.Resource != fabricWirelessMulticastResource(path, enabled, deps) {
		return false, errConfigurationStale
	}
	return deps.state.enabled == enabled, nil
}
