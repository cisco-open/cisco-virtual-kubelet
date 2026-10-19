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

type fabricDeviceDependencies struct {
	native         fabricDeviceConfiguration
	target         Device
	physical       []Device
	before         map[string]fabricDeviceConfiguration
	digest         string
	recoveryDigest string
	deferredParent bool
}

// A source-derived parent proposal is preflight evidence only. It cannot be
// supplied to native planning, mutation admission, recovery or final verification.
type fabricDevicesParentProposal struct {
	fabricID string
	members  map[string]fabricDeviceConfiguration
}

// Resolve the complete local fabric footprint, including the incoming device.
// This is preflight evidence, not provisioning completion or mutation admission.
func (c *client) fabricDeviceDependencies(ctx context.Context, want fabricDeviceIntent) (*fabricDeviceDependencies, error) {
	return c.collectFabricDeviceDependencies(ctx, want, false)
}

// In existingRoles mode native roles and border settings are prerequisites to
// preserve, never desired membership changes. Only embedded-WLC uses this mode.
func (c *client) collectFabricDeviceDependencies(ctx context.Context, want fabricDeviceIntent, existingRoles bool) (*fabricDeviceDependencies, error) {
	return c.collectFabricDeviceDependenciesForCreateSet(ctx, want, existingRoles, nil)
}

// Bulk observation includes all incoming physical IDs, and excludes only their
// expected new memberships from the recovery baseline. Singular callers retain
// the original target-only recovery semantics.
func (c *client) collectFabricDeviceDependenciesForCreateSet(ctx context.Context, want fabricDeviceIntent, existingRoles bool, createSet []string) (*fabricDeviceDependencies, error) {
	return c.collectFabricDeviceDependenciesPreflight(ctx, want, existingRoles, createSet, nil)
}

func (c *client) collectFabricDeviceDependenciesPreflight(ctx context.Context, want fabricDeviceIntent, existingRoles bool, createSet []string, proposal *fabricDevicesParentProposal) (*fabricDeviceDependencies, error) {
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	target, err := resolveConfigurationDevice(want.inventory.reference, inventory)
	if err != nil {
		return nil, err
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, site := range sites {
		byPath[site.NameHierarchy] = site
	}
	placement, exists := byPath[want.inventory.site]
	if !exists || !validAPIID(placement.ID) {
		return nil, errConfigurationBusy
	}
	parent, exists := byPath[want.fabricSite]
	if !exists || !validAPIID(parent.ID) {
		return nil, errConfigurationBusy
	}
	fabrics, err := c.fabricSites(ctx)
	if err != nil {
		return nil, err
	}
	fabric, exists := fabrics[parent.ID]
	if !exists {
		return nil, errConfigurationBusy
	}
	fabricID := fabric.ID
	zone := fabricZone{}
	if want.fabricZone != "" {
		zoneSite, exists := byPath[want.fabricZone]
		if !exists {
			return nil, errConfigurationBusy
		}
		zones, err := c.fabricZones(ctx)
		if err != nil {
			return nil, err
		}
		zone, exists = zones[zoneSite.ID]
		if !exists {
			return nil, errConfigurationBusy
		}
		fabricID = zone.ID
	}
	assigned, err := c.assignedDevices(ctx, placement.ID)
	if err != nil {
		return nil, err
	}
	if !assigned[target.ID] {
		return nil, errConfigurationBusy
	}
	if want.inventory.role != "" && (target.Role != want.inventory.role || target.RoleSource != want.inventory.roleSource) {
		return nil, errConfigurationBusy
	}
	provisioned, ready, err := c.wiredProvisionedDevice(ctx, target.ID)
	if err != nil {
		return nil, err
	}
	if !ready || provisioned.SiteID != placement.ID {
		return nil, errConfigurationBusy
	}
	before, err := c.fabricDeviceConfigurations(ctx, fabricID)
	if err != nil {
		return nil, err
	}
	if existingRoles {
		if len(want.roles) != 0 || want.border != nil {
			return nil, errConfigurationTransition
		}
		member, exists := before[target.ID]
		if !exists {
			return nil, errConfigurationBusy
		}
		want.roles = slices.Clone(member.Roles)
		want.border = member.Border
	}
	// Zone membership depends on the same physical device already belonging to
	// its parent fabric; include the parent population in the eventual footprint.
	parentMembers := map[string]fabricDeviceConfiguration{}
	var futureParents map[string]fabricDeviceConfiguration
	deferredParent := false
	if want.fabricZone != "" {
		parentMembers, err = c.fabricDeviceConfigurations(ctx, fabric.ID)
		if err != nil {
			return nil, err
		}
		if proposal != nil {
			if proposal.fabricID != fabric.ID || existingRoles {
				return nil, errConfigurationStale
			}
			for id, desired := range proposal.members {
				if desired.NetworkDeviceID != id || desired.FabricID != fabric.ID {
					return nil, errConfigurationStale
				}
				if _, err := canonicalFabricDevice(desired, true); err != nil {
					return nil, err
				}
				if actual, exists := parentMembers[id]; exists {
					if !fabricDeviceConfigurationMatches(desired, actual) {
						return nil, errConfigurationStale
					}
				} else {
					deferredParent = true
				}
			}
			if deferredParent {
				futureParents = proposal.members
			}
		}
		member, exists := parentMembers[target.ID]
		if !exists && futureParents != nil {
			member, exists = futureParents[target.ID]
		}
		if !exists {
			return nil, errConfigurationBusy
		}
		for _, role := range want.roles {
			if !slices.Contains(member.Roles, role) {
				return nil, errConfigurationTransition
			}
		}
	}
	ids := append([]string{target.ID}, createSet...)
	for id := range before {
		ids = append(ids, id)
	}
	for id := range parentMembers {
		ids = append(ids, id)
	}
	for id := range futureParents {
		ids = append(ids, id)
	}
	physical, err := bindMulticastRPDevices(ids, inventory)
	if err != nil {
		return nil, err
	}
	for _, device := range physical {
		if device.ID == target.ID {
			target = device
		}
	}
	native, err := canonicalFabricDevice(fabricDeviceConfiguration{NetworkDeviceID: target.ID, FabricID: fabricID, Roles: want.roles, Border: want.border}, true)
	if err != nil {
		return nil, err
	}
	// Hash placement/parent/zone identity plus the complete physical and native
	// baseline; unrelated site geometry and inventory devices are not dependencies.
	evidence := struct {
		Placement, Parent     Site
		Zone                  fabricZone
		Fabric                fabricSite
		Native                fabricDeviceConfiguration
		Physical              []Device
		Before, ParentMembers map[string]fabricDeviceConfiguration
		Provisioned           wiredProvisionedDevice
		FutureParents         map[string]fabricDeviceConfiguration `json:"futureParents,omitempty"`
	}{placement, parent, zone, fabric, native, physical, before, parentMembers, provisioned, futureParents}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return nil, err
	}

	// Creation may add the target membership, but no other baseline change is
	// an expected result. Keep those dependencies stable across recovery.
	selected := map[string]bool{target.ID: true}
	for _, id := range createSet {
		selected[id] = true
	}
	peers := map[string]fabricDeviceConfiguration{}
	for id, row := range before {
		if !selected[id] {
			peers[id] = row
		}
	}
	evidence.Before = peers
	recovery, err := json.Marshal(evidence)
	if err != nil {
		return nil, err
	}
	return &fabricDeviceDependencies{native: native, target: target, physical: physical, before: before, digest: fmt.Sprintf("%x", sha256.Sum256(raw)), recoveryDigest: fmt.Sprintf("%x", sha256.Sum256(recovery)), deferredParent: deferredParent}, nil
}
