// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sync/atomic"
)

// Hash both complete mapping sets separately so recovery can compare the
// expected result without reconstructing a baseline that the PUT has replaced.
func fabricVLANSSIDResource(path string, deps *fabricVLANSSIDDependencies) (string, error) {
	scope, err := json.Marshal([]string{path, deps.siteID, deps.fabricID})
	if err != nil {
		return "", err
	}
	before, err := json.Marshal(deps.before)
	if err != nil {
		return "", err
	}
	after, err := json.Marshal(deps.payload)
	if err != nil {
		return "", err
	}
	name := ""
	if deps.claim != nil {
		if len(deps.claim.SSIDs) != 1 {
			return "", errInvalidResponse
		}
		name = deps.claim.SSIDs[0].Name
	}
	claim, err := json.Marshal(deps.claim)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("fabric-vlan-ssid-update:%x:%x:%x:%x:%x", sha256.Sum256(scope), sha256.Sum256(before), sha256.Sum256(after), sha256.Sum256([]byte(name)), sha256.Sum256(claim)), nil
}

// The coordinator must supply receipt-derived ownership and a journal-backed
// one-use guard. This private planner does not expose Apply or grant authority.
func (c *client) planGuardedFabricVLANSSIDs(ctx context.Context, path string, want []fabricVLANSSID, owned map[string]fabricVLANSSIDOwned, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	// Freeze caller-owned inputs before capturing them in the submission closure.
	desired, err := canonicalFabricVLANSSIDs(want)
	if err != nil {
		return nil, err
	}
	ownership := make(map[string]fabricVLANSSIDOwned, len(owned))
	for key, value := range owned {
		if value.SecurityGroup != nil {
			tag := *value.SecurityGroup
			value.SecurityGroup = &tag
		}
		ownership[key] = value
	}
	return c.planFabricVLANSSIDWrite(ctx, path, func(ctx context.Context) (*fabricVLANSSIDDependencies, error) {
		return c.vlanSSIDDependencies(ctx, path, desired, ownership)
	}, guard)
}

func (c *client) planFabricVLANSSIDWrite(ctx context.Context, path string, collect func(context.Context) (*fabricVLANSSIDDependencies, error), guard func(context.Context) error) (*configurationWrite, error) {
	if collect == nil || guard == nil || !validSitePath(path) || path == "Global" {
		return nil, errConfigurationDeviceAdmission
	}
	deps, err := collect(ctx)
	if err != nil {
		return nil, err
	}
	if deps == nil || len(deps.digest) != 64 || !validAPIID(deps.siteID) {
		return nil, errInvalidResponse
	}
	endpoint, err := fabricVLANSSIDPath(deps.fabricID)
	if err != nil {
		return nil, err
	}
	if reflect.DeepEqual(deps.before, deps.payload) {
		return nil, nil
	}
	if deps.claim == nil {
		return nil, errInvalidResponse
	}
	body, err := json.Marshal(deps.payload)
	if err != nil {
		return nil, err
	}
	resource, err := fabricVLANSSIDResource(path, deps)
	if err != nil {
		return nil, err
	}
	baseline := deps.digest
	var used atomic.Bool
	return &configurationWrite{resource: resource, action: "Update", identity: configurationResourceIdentity{target: deps.fabricID, parent: deps.siteID}, submit: func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		fresh, err := collect(ctx)
		if err != nil {
			return Task{}, err
		}
		if fresh == nil || fresh.digest != baseline {
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
