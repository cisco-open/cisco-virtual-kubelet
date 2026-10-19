// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func (c *client) reportPlannedAPs(ctx context.Context, desired []plannedAPIntent, sites []Site) ([]configapi.NetworkControllerDriftEntry, error) {
	byPath := map[string]Site{}
	for _, site := range sites {
		byPath[site.NameHierarchy] = site
	}
	inventories := map[string]map[string]plannedAPObservation{}
	drift := []configapi.NetworkControllerDriftEntry{}
	for _, want := range desired {
		change := configapi.NetworkControllerDriftChangeAdd
		if floor, exists := byPath[want.floor]; exists {
			if floor.Type != "floor" || !validAPIID(floor.ID) {
				return nil, errInvalidResponse
			}
			inventory, read := inventories[floor.ID]
			if !read {
				var err error
				inventory, err = c.plannedAPPositions(ctx, floor.ID)
				if err != nil {
					return nil, err
				}
				inventories[floor.ID] = inventory
			}
			if got, exists := inventory[want.payload["name"].(string)]; exists {
				change = configapi.NetworkControllerDriftChangeUpdate
				if got.Type == want.payload["type"] {
					_, changed, err := plannedAPUpdatePayload(want, got)
					if err != nil && !errors.Is(err, errPlannedAPRadioReplacement) {
						return nil, err
					}
					if err == nil && !changed {
						continue
					}
				}
			}
		}
		drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "sites", Resource: plannedAPResource(want), Path: "/planned_access_points", Change: change})
	}
	return drift, nil
}
