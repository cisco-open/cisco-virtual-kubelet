// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
)

type fabricReplicationDependencies struct {
	state                fabricReplicationState
	physical             []Device
	digest, resultDigest string
}

// Replication follows multicast VN configuration in the pinned module. Bind all
// current native VNs, their L3 membership, RP/pool evidence and physical fabric
// members so a site-wide mode change cannot race those dependencies.
func (c *client) replicationDependencies(ctx context.Context, path string) (*fabricReplicationDependencies, error) {
	state, err := c.fabricReplicationState(ctx, path)
	if err != nil {
		return nil, err
	}
	native, err := c.fabricMulticastVNs(ctx)
	if err != nil {
		return nil, err
	}
	networks, err := c.fabricL3Networks(ctx)
	if err != nil {
		return nil, err
	}
	selected := map[string]fabricL3Network{}
	for key, row := range native {
		if key.fabricID != state.fabricID {
			continue
		}
		network, exists := networks[row.Network]
		if !exists || !validAPIID(network.ID) || !slices.Contains(network.Fabrics, state.fabricID) {
			return nil, errConfigurationBusy
		}
		network.Fabrics = slices.Clone(network.Fabrics)
		slices.Sort(network.Fabrics)
		selected[row.Network] = network
	}
	pools, err := c.loadAnycastPoolInventory(ctx)
	if err != nil {
		return nil, err
	}
	memberships, err := c.fabricDeviceMemberships(ctx, state.fabricID)
	if err != nil {
		return nil, err
	}
	evidence, err := validateFabricMulticastSite(state.fabricID, state.siteID, native, nil, pools, memberships)
	if err != nil {
		return nil, err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	members, physical, err := bindFabricMutationTargets(state.fabricID, memberships, inventory)
	if err != nil {
		return nil, err
	}
	if len(members) == 0 {
		return nil, errConfigurationBusy
	}
	retained := map[string]map[string]any{}
	for key, row := range native {
		if key.fabricID == state.fabricID {
			retained[row.Network] = row.fields
		}
	}
	raw, err := json.Marshal([]any{state.siteID, state.fabricID, selected, retained, evidence.poolDigests, members, physical})
	if err != nil {
		return nil, err
	}
	result := fmt.Sprintf("%x", sha256.Sum256(raw))
	raw, err = json.Marshal([]any{result, state.exists, state.mode})
	if err != nil {
		return nil, err
	}
	return &fabricReplicationDependencies{state: state, physical: physical, resultDigest: result, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
}

func fabricReplicationGroupResource(path, mode string, deps *fabricReplicationDependencies) string {
	return fabricReplicationResultResource(path, mode, deps) + ":" + deps.digest
}
func fabricReplicationResultResource(path, mode string, deps *fabricReplicationDependencies) string {
	raw, _ := json.Marshal([]string{path, mode, deps.resultDigest})
	return fmt.Sprintf("fabric-replication-update:%x", sha256.Sum256(raw))
}

func (c *client) planGuardedFabricReplicationGroup(ctx context.Context, path, mode string, guard func(context.Context) error) (*configurationWrite, error) {
	if guard == nil {
		return nil, errConfigurationDeviceAdmission
	}
	before, err := c.replicationDependencies(ctx, path)
	if err != nil {
		return nil, err
	}
	write, err := c.planGuardedFabricReplication(ctx, path, mode, func(ctx context.Context) error {
		fresh, err := c.replicationDependencies(ctx, path)
		if err != nil {
			return err
		}
		if fresh.digest != before.digest {
			return errConfigurationStale
		}
		return guard(ctx)
	})
	if err != nil {
		return nil, err
	}
	// Bracket native planning too, including no-op observations.
	after, err := c.replicationDependencies(ctx, path)
	if err != nil {
		return nil, err
	}
	if before.digest != after.digest {
		return nil, errConfigurationStale
	}
	if write != nil {
		if write.identity.target != before.state.fabricID || write.identity.parent != before.state.siteID {
			return nil, errConfigurationStale
		}
		write.resource = fabricReplicationGroupResource(path, mode, before)
	}
	return write, nil
}
