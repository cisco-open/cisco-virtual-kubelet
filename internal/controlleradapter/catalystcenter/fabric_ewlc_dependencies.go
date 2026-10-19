// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
)

type fabricEWLCFabricDependencies struct {
	foundation *fabricDeviceDependencies
	scope      *fabricEWLCScope
	target     Device
	observed   *fabricEWLCSetting
	locations  *fabricEWLCLocations
	digest     string
}

// Reuse existing site/zone, assignment, provisioning and fabric configuration
// dependencies. Embedded enablement cannot create or repair those prerequisites
// implicitly. AP placement is checked read-only. This collector does not attest
// installed wireless-package or platform support; those require lab qualification.
func (c *client) fabricEWLCFabricDependencies(ctx context.Context, want fabricEWLCIntent) (*fabricEWLCFabricDependencies, error) {
	foundation, err := c.collectFabricDeviceDependencies(ctx, want.device, len(want.device.roles) == 0)
	if err != nil {
		return nil, err
	}
	scope, err := c.fabricEWLCScope(ctx, foundation.native.FabricID)
	if err != nil {
		return nil, err
	}
	bound, err := bindFabricEWLCFoundation(foundation, scope, want.settings)
	if err != nil {
		return nil, err
	}
	bound.locations, err = c.fabricEWLCLocations(ctx, bound.target.ID, want.locations)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal([]string{bound.digest, bound.locations.digest})
	if err != nil {
		return nil, err
	}
	bound.digest = fmt.Sprintf("%x", sha256.Sum256(raw))
	return bound, nil
}

func bindFabricEWLCFoundation(foundation *fabricDeviceDependencies, scope *fabricEWLCScope, want fabricEWLCSettings) (*fabricEWLCFabricDependencies, error) {
	if foundation == nil || scope == nil || foundation.digest == "" || scope.digest == "" || foundation.native.FabricID != scope.fabricID || foundation.native.NetworkDeviceID != foundation.target.ID {
		return nil, errConfigurationStale
	}
	observed, exists := foundation.before[foundation.target.ID]
	if !exists || !fabricDeviceConfigurationMatches(foundation.native, observed) {
		return nil, errConfigurationBusy
	}
	if len(scope.members) != len(foundation.before) {
		return nil, errConfigurationStale
	}
	for _, member := range scope.members {
		prior, exists := foundation.before[member.NetworkDeviceID]
		if !exists || prior.ID != member.ID || prior.FabricID != member.FabricID || !slices.Equal(prior.Roles, member.Roles) {
			return nil, errConfigurationStale
		}
	}
	// The foundation may additionally include parent-fabric devices for a zone.
	// Retain that larger footprint; every device in this scope must match it.
	for _, device := range scope.physical {
		matches := 0
		for _, prior := range foundation.physical {
			if prior.ID == device.ID {
				if !reflect.DeepEqual(prior, device) {
					return nil, errConfigurationStale
				}
				matches++
			}
		}
		if matches != 1 {
			return nil, errConfigurationStale
		}
	}
	target, row, err := scope.target(foundation.target.ID, want)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(target, foundation.target) {
		return nil, errConfigurationStale
	}
	raw, err := json.Marshal([]any{foundation.digest, scope.digest, want.enabled, want.rolling, want.percentage})
	if err != nil {
		return nil, err
	}
	return &fabricEWLCFabricDependencies{foundation: foundation, scope: scope, target: target, observed: row, digest: fmt.Sprintf("%x", sha256.Sum256(raw))}, nil
}
