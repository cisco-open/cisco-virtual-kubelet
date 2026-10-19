// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
)

type fabricPendingEventsSnapshot struct {
	site    fabricSiteState
	events  []fabricPendingEvent
	members []fabricDeviceMembership
	devices []Device
}

// Resolve through the existing site executor so event work cannot bypass the
// parent site's desired state. This is native evidence, not ownership/admission.
func (c *client) pendingFabricEventsSnapshot(ctx context.Context, want fabricSiteIntent) (*fabricPendingEventsSnapshot, error) {
	if !validSitePath(want.path) || want.path == "Global" || !validFabricAuthentication(want.authentication) {
		return nil, errInvalidResponse
	}
	executor := &fabricSiteExecutor{client: c, desired: []fabricSiteIntent{want}}
	states, err := executor.observe(ctx)
	if err != nil {
		return nil, err
	}
	if len(states) != 1 || !states[0].exists || !fabricSiteMatches(want, states[0].actual) {
		return nil, errConfigurationBusy
	}
	events, err := c.fabricPendingEvents(ctx, states[0].actual.ID)
	if err != nil {
		return nil, err
	}
	membership, err := c.fabricDeviceMemberships(ctx, states[0].actual.ID)
	if err != nil {
		return nil, err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	members, devices, err := bindFabricMutationTargets(states[0].actual.ID, membership, inventory)
	if err != nil {
		return nil, err
	}
	return &fabricPendingEventsSnapshot{site: states[0], events: events, members: members, devices: devices}, nil
}

func (c *client) revalidatePendingFabricEvents(ctx context.Context, want fabricSiteIntent, previous *fabricPendingEventsSnapshot) error {
	if previous == nil {
		return errConfigurationStale
	}
	current, err := c.pendingFabricEventsSnapshot(ctx, want)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(previous, current) {
		return errConfigurationStale
	}
	return nil
}
