// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
)

type fabricSDAHandoffDependencies struct {
	binding        fabricSDAHandoffBinding
	physical       []Device
	before         []fabricSDAHandoff
	digest         string
	recoveryDigest string
}

// Observe local and transit-linked fabric populations plus control planes.
// This dependency snapshot is not mutation admission or device convergence.
func (c *client) fabricSDAHandoffDependencies(ctx context.Context, want fabricSDAHandoffDeploymentIntent) (*fabricSDAHandoffDependencies, error) {
	if want.handoff.Device != want.device.inventory.reference.Name || want.device.fabricZone != "" || (want.device.state != "PROVISION" && want.device.state != "REPROVISION") {
		return nil, errInvalidResponse
	}
	deps, err := c.fabricDeviceDependencies(ctx, want.device)
	if err != nil {
		return nil, err
	}
	actual, found := deps.before[deps.target.ID]
	if !found || !fabricDeviceConfigurationMatches(deps.native, actual) {
		return nil, errConfigurationBusy
	}
	transits, err := c.fabricTransits(ctx)
	if err != nil {
		return nil, err
	}
	binding, err := bindFabricSDAHandoff(want.handoff, want.device.inventory.reference.Name, actual, transits)
	if err != nil {
		return nil, err
	}
	participants, participantDigest, participantRecovery, err := c.fabricSDAParticipantEvidence(ctx, actual.FabricID, binding.transit.ID, &binding.desired)
	if err != nil {
		return nil, err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	ids := append([]string(nil), binding.transit.SDA.ControlPlanes...)
	ids = append(ids, participants...)
	for _, device := range deps.physical {
		ids = append(ids, device.ID)
	}
	physical, err := bindMulticastRPDevices(ids, inventory)
	if err != nil {
		return nil, err
	}
	byID := map[string]Device{}
	for _, device := range physical {
		byID[device.ID] = device
	}
	for _, device := range deps.physical {
		if !reflect.DeepEqual(device, byID[device.ID]) {
			return nil, errConfigurationStale
		}
	}
	before, err := c.fabricSDAHandoffs(ctx, actual.FabricID, actual.NetworkDeviceID)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal([]any{deps.digest, binding.digest, participantDigest, physical, before})
	if err != nil {
		return nil, errInvalidResponse
	}
	baseline := []fabricSDAHandoff{}
	for _, row := range before {
		if !fabricSDAHandoffMatches(binding.desired, row) {
			baseline = append(baseline, row)
		}
	}
	recovery, err := json.Marshal([]any{deps.digest, binding.digest, participantRecovery, physical, baseline})
	if err != nil {
		return nil, errInvalidResponse
	}
	return &fabricSDAHandoffDependencies{binding: binding, physical: physical, before: before, digest: fmt.Sprintf("%x", sha256.Sum256(raw)), recoveryDigest: fmt.Sprintf("%x", sha256.Sum256(recovery))}, nil
}
