// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

type fabricWirelessMulticastState struct {
	siteID, fabricID string
	enabled          bool
}

func (c *client) observeFabricWirelessMulticast(ctx context.Context, path string) (fabricWirelessMulticastState, error) {
	zero := fabricWirelessMulticastState{}
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
	state := states[0]
	enabled, err := c.fabricWirelessMulticast(ctx, state.actual.ID)
	if err != nil {
		return zero, err
	}
	return fabricWirelessMulticastState{state.siteID, state.actual.ID, enabled}, nil
}

func (c *client) reportFabricWirelessMulticast(ctx context.Context, path string, enabled bool) ([]configapi.NetworkControllerDriftEntry, error) {
	before, err := c.observeFabricWirelessMulticast(ctx, path)
	if err != nil {
		return nil, err
	}
	after, err := c.observeFabricWirelessMulticast(ctx, path)
	if err != nil {
		return nil, err
	}
	if before != after {
		return nil, errConfigurationStale
	}
	if before.enabled == enabled {
		return nil, nil
	}
	// A boolean setting is updated; absent readback is not a creatable resource.
	identity, _ := json.Marshal([]string{path, before.siteID, before.fabricID})
	return []configapi.NetworkControllerDriftEntry{{
		Section: "fabric", Resource: fmt.Sprintf("fabric-wireless-multicast:%x", sha256.Sum256(identity)),
		Path: "/fabric_sites/multicast/wireless_multicast_enabled", Change: configapi.NetworkControllerDriftChangeUpdate,
	}}, nil
}
