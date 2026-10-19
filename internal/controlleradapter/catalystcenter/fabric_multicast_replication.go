// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const fabricMulticastPath = "/dna/intent/api/v1/sda/multicast"

type fabricMulticastReplication struct {
	FabricID string `json:"fabricId"`
	Mode     string `json:"replicationMode"`
}

func validFabricReplicationMode(mode string) bool {
	return mode == "HEADEND_REPLICATION" || mode == "NATIVE_MULTICAST"
}

// Native replication configuration is keyed by fabricId, not a separate object ID.
func (c *client) fabricMulticastReplicationModes(ctx context.Context) (map[string]fabricMulticastReplication, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryQueryWithGet(ctx, get, fabricMulticastPath, nil, func(s fabricMulticastReplication) string { return s.FabricID })
	if err != nil {
		return nil, err
	}
	out := map[string]fabricMulticastReplication{}
	for _, row := range rows {
		if !validAPIID(row.FabricID) || !validFabricReplicationMode(row.Mode) {
			return nil, errInvalidResponse
		}
		if _, exists := out[row.FabricID]; exists {
			return nil, errInvalidResponse
		}
		out[row.FabricID] = row
	}
	return out, nil
}

type fabricReplicationState struct {
	siteID, fabricID string
	mode             string
	exists           bool
}

func (c *client) fabricReplicationState(ctx context.Context, path string) (fabricReplicationState, error) {
	zero := fabricReplicationState{}
	if !validSitePath(path) || path == "Global" {
		return zero, errInvalidResponse
	}
	states, err := (&fabricSiteExecutor{client: c, desired: []fabricSiteIntent{{path: path}}}).observe(ctx)
	if err != nil {
		return zero, err
	}
	if len(states) != 1 || !states[0].exists {
		return zero, errConfigurationBusy
	}
	modes, err := c.fabricMulticastReplicationModes(ctx)
	if err != nil {
		return zero, err
	}
	state := states[0]
	observed, exists := modes[state.actual.ID]
	return fabricReplicationState{siteID: state.siteID, fabricID: state.actual.ID, mode: observed.Mode, exists: exists}, nil
}

func fabricReplicationResource(path, mode string, state fabricReplicationState) string {
	raw, _ := json.Marshal([]string{path, mode, state.siteID, state.fabricID})
	return fmt.Sprintf("fabric-replication:%x", sha256.Sum256(raw))
}

// Runtime callers wrap this primitive with multicast dependency validation and
// the shared physical-device group submission permit.
func (c *client) planGuardedFabricReplication(ctx context.Context, path, mode string, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	if !validFabricReplicationMode(mode) {
		return nil, errInvalidResponse
	}
	before, err := c.fabricReplicationState(ctx, path)
	if err != nil {
		return nil, err
	}
	if before.exists && before.mode == mode {
		return nil, nil
	}
	body, err := json.Marshal([]fabricMulticastReplication{{FabricID: before.fabricID, Mode: mode}})
	if err != nil {
		return nil, err
	}
	var used atomic.Bool
	return &configurationWrite{
		resource: fabricReplicationResource(path, mode, before), action: "Update",
		identity: configurationResourceIdentity{target: before.fabricID, parent: before.siteID},
		submit: func(ctx context.Context) (Task, error) {
			if !used.CompareAndSwap(false, true) {
				return Task{}, errConfigurationTransition
			}
			latest, err := c.fabricReplicationState(ctx, path)
			if err != nil {
				return Task{}, err
			}
			if latest != before {
				return Task{}, errConfigurationStale
			}
			if err := guard(ctx); err != nil {
				return Task{}, err
			}
			raw, err := c.mutate(ctx, http.MethodPut, fabricMulticastPath, body)
			if err != nil {
				return Task{}, err
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				return Task{}, errInvalidResponse
			}
			return Task{ID: receipt.Response.ID}, nil
		},
	}, nil
}

func (c *client) verifyFabricReplication(ctx context.Context, path, mode string, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if !validFabricReplicationMode(mode) || op == nil || op.Action != "Update" || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	state, err := c.fabricReplicationState(ctx, path)
	if err != nil {
		return false, err
	}
	if state.fabricID != op.TargetID || state.siteID != op.ParentID || op.Resource != fabricReplicationResource(path, mode, state) {
		return false, errConfigurationStale
	}
	return state.exists && state.mode == mode, nil
}
