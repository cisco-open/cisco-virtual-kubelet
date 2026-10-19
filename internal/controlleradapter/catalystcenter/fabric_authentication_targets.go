// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
	"strings"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type fabricAuthPhysical struct {
	state    fabricAuthState
	physical []Device
	// Digest excludes the mutable authentication profile, so the same physical
	// footprint can later be checked against an accepted task's result.
	digest string
	// Observation binds profile values too while bracketing managed resolution.
	observation string
}

func (c *client) fabricAuthenticationPhysical(ctx context.Context, want fabricAuthIntent) (*fabricAuthPhysical, error) {
	states, err := c.observeFabricAuthentication(ctx, []fabricAuthIntent{want})
	if err != nil {
		return nil, err
	}
	if len(states) != 1 || !states[0].exists {
		return nil, errConfigurationBusy
	}
	state := states[0]
	hierarchy, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	paths := map[string]string{}
	for _, site := range hierarchy {
		paths[site.ID] = site.NameHierarchy
	}
	if paths[state.siteID] != want.site.path {
		return nil, errConfigurationStale
	}
	fabrics, err := c.fabricSites(ctx)
	if err != nil {
		return nil, err
	}
	if root, ok := fabrics[state.siteID]; !ok || root.ID != state.fabricID || !fabricSiteMatches(want.site, root) {
		return nil, errConfigurationStale
	}
	zones, err := c.fabricZones(ctx)
	if err != nil {
		return nil, err
	}
	// Conservatively include every descendant zone owned by this nearest fabric,
	// regardless of its selected authentication mode. Never omit a potential
	// affected device on the assumption that inheritance will protect it.
	scopes := map[string]string{state.fabricID: state.siteID}
	zoneEvidence := []fabricZone{}
	for siteID, zone := range zones {
		path, ok := paths[siteID]
		if !ok {
			return nil, errConfigurationStale
		}
		if !strings.HasPrefix(path, want.site.path+"/") {
			continue
		}
		nearest := ""
		for fabricSiteID := range fabrics {
			parent, ok := paths[fabricSiteID]
			if !ok {
				return nil, errConfigurationStale
			}
			if strings.HasPrefix(path, parent+"/") && len(parent) > len(nearest) {
				nearest = parent
			}
		}
		if nearest != want.site.path {
			continue
		}
		if _, collision := fabrics[siteID]; collision || scopes[zone.ID] != "" {
			return nil, errConfigurationStale
		}
		scopes[zone.ID] = siteID
		zoneEvidence = append(zoneEvidence, zone)
	}
	sort.Slice(zoneEvidence, func(i, j int) bool { return zoneEvidence[i].ID < zoneEvidence[j].ID })
	ids := []string{}
	members := []fabricDeviceMembership{}
	seenMembers := map[string]bool{}
	for scopeID := range scopes {
		rows, err := c.fabricDeviceMemberships(ctx, scopeID)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			if seenMembers[row.ID] {
				return nil, errConfigurationStale
			}
			seenMembers[row.ID] = true
			members = append(members, row)
			ids = append(ids, row.NetworkDeviceID)
		}
	}
	if len(ids) == 0 {
		return nil, errConfigurationBusy
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	physical, err := bindMulticastRPDevices(ids, inventory)
	if err != nil {
		return nil, err
	}
	digest := fabricAuthDigest([]any{state.siteID, state.fabricID, scopes, zoneEvidence, members, physical})
	return &fabricAuthPhysical{state: state, physical: physical, digest: digest, observation: fabricAuthDigest([]string{digest, state.profile.ID, fabricAuthProfileDigest(state.profile)})}, nil
}

func (c *client) fabricAuthenticationTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricAuthIntent) (*fabricAuthPhysical, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricAuthPhysical], error) {
		state, err := c.fabricAuthenticationPhysical(ctx, want)
		if err != nil {
			return configurationPhysicalObservation[*fabricAuthPhysical]{}, err
		}
		return configurationPhysicalObservation[*fabricAuthPhysical]{state: state, physical: state.physical, digest: state.observation}, nil
	})
}

// Preparation has observation authority only. Its returned closure is always
// disabled, even though the shared private planner requires an authority hook.
// Dispatch must rebuild under a real retained group permit.
func (c *client) planManagedFabricAuthentication(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricAuthIntent) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	before, targets, err := c.fabricAuthenticationTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	write, err := c.planFabricAuthentication(ctx, []fabricAuthIntent{want}, func(context.Context) error { return nil })
	if err != nil {
		return nil, nil, err
	}
	after, current, err := c.fabricAuthenticationTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	if before.observation != after.observation || !authPreservedFieldsEqual(before.state.profile, after.state.profile) || !reflect.DeepEqual(targets, current) {
		return nil, nil, errConfigurationStale
	}
	if write != nil {
		if write.identity.target != before.state.profile.ID || write.identity.parent != before.state.fabricID || write.resource != want.resource(before.state.siteID) {
			return nil, nil, errConfigurationStale
		}
		write.resource = fabricAuthManagedResource(want, before.state, before.digest)
		write.submit = func(context.Context) (Task, error) { return Task{}, errConfigurationDeviceAdmission }
	}
	return write, targets, nil
}
