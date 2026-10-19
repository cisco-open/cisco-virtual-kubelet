// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type fabricVLANSSIDDependencies struct {
	siteID, fabricID string
	claim            *fabricVLANSSID
	before, payload  []fabricVLANSSID
	members          []fabricDeviceMembership
	physical         []Device
	networks         []fabricPortAssignmentVLANBinding
	profiles         []fabricSSIDProfileBinding
	controllers      []fabricSSIDControllerBinding
	profileContent   map[string]string
	digest           string
}

// A PUT replaces the entire native mapping set. Check the complete prospective
// payload, including retained entries, against current design and provisioning.
// This evidence grants no mutation authority and does not adopt existing rows.
func (c *client) vlanSSIDDependencies(ctx context.Context, path string, want []fabricVLANSSID, owned map[string]fabricVLANSSIDOwned) (*fabricVLANSSIDDependencies, error) {
	state, err := c.observeFabricVLANSSIDs(ctx, path)
	if err != nil {
		return nil, err
	}
	members, err := c.fabricDeviceMemberships(ctx, state.fabricID)
	if err != nil {
		return nil, err
	}
	snapshot, err := c.wirelessInventoryDependencies(ctx)
	if err != nil {
		return nil, err
	}
	l2, err := c.fabricL2Networks(ctx)
	if err != nil {
		return nil, err
	}
	gateways, err := c.anycastGateways(ctx)
	if err != nil {
		return nil, err
	}
	return resolveFabricVLANSSIDDependencies(path, want, owned, state, members, snapshot, l2, gateways,
		func(id string) (map[string]wirelessSSIDMetadata, error) { return c.wirelessSSIDInventory(ctx, id) },
		func(id string) (wirelessDeviceProvisionStatus, error) {
			return c.wirelessDeviceProvisionStatus(ctx, id)
		},
		func(profiles map[string]string) (map[string]string, error) {
			return c.validateWirelessPlacementProfiles(ctx, snapshot, profiles)
		}, func() ([]fabricSSIDControllerBinding, error) {
			return c.fabricEmbeddedSSIDControllers(ctx, path, state.fabricID, members, snapshot.devices, snapshot.sites)
		})
}

func resolveFabricVLANSSIDDependencies(path string, want []fabricVLANSSID, owned map[string]fabricVLANSSIDOwned, state *fabricVLANSSIDObservation, members map[string]fabricDeviceMembership, snapshot wirelessInventoryDependencies, l2 map[fabricL2Key]fabricL2Network, gateways map[anycastGatewayKey]anycastGateway, definitions func(string) (map[string]wirelessSSIDMetadata, error), status func(string) (wirelessDeviceProvisionStatus, error), content func(map[string]string) (map[string]string, error), embedded func() ([]fabricSSIDControllerBinding, error)) (*fabricVLANSSIDDependencies, error) {
	if state == nil || !validAPIID(state.siteID) || !validAPIID(state.fabricID) || content == nil {
		return nil, errInvalidResponse
	}
	site, exists := snapshot.sites[path]
	if !exists || site.ID != state.siteID || site.NameHierarchy != path {
		return nil, errConfigurationStale
	}
	selected, err := selectFabricVLANSSIDs(want, state.mappings, owned)
	if err != nil {
		return nil, err
	}
	before, err := canonicalFabricVLANSSIDs(state.mappings)
	if err != nil {
		return nil, err
	}
	out := &fabricVLANSSIDDependencies{siteID: state.siteID, fabricID: state.fabricID, before: before, payload: selected.payload}
	ids := make([]string, 0, len(members))
	for id, member := range members {
		if id != member.NetworkDeviceID || member.FabricID != state.fabricID || !validAPIID(member.ID) {
			return nil, errConfigurationStale
		}
		ids = append(ids, id)
		out.members = append(out.members, member)
	}
	sort.Slice(out.members, func(i, j int) bool { return out.members[i].NetworkDeviceID < out.members[j].NetworkDeviceID })
	out.physical, err = bindMulticastRPDevices(ids, snapshot.devices)
	if err != nil {
		return nil, err
	}
	out.networks, err = resolveFabricVLANSSIDNetworks(state.fabricID, out.payload, l2, gateways)
	if err != nil {
		return nil, err
	}
	required := map[string]bool{}
	for _, row := range out.payload {
		for _, ssid := range row.SSIDs {
			required[ssid.Name] = true
		}
	}
	if len(required) == 0 {
		return nil, errConfigurationBusy
	}
	out.profiles, err = resolveFabricSSIDProfiles(path, required, snapshot, definitions)
	if err != nil {
		return nil, err
	}
	selectedProfiles := map[string]string{}
	for _, profile := range out.profiles {
		selectedProfiles["profile:"+profile.ProfileName] = profile.ProfileID
	}
	out.profileContent, err = content(selectedProfiles)
	if err != nil {
		return nil, err
	}
	// SSID-bearing profiles require model configuration, following the existing
	// wireless inventory provisioning executor's readiness rule.
	standalone := false
	for _, member := range members {
		standalone = standalone || slices.Contains(member.Roles, "WIRELESS_CONTROLLER_NODE")
	}
	if !standalone && embedded != nil {
		out.controllers, err = embedded()
		if err == nil && len(out.controllers) == 0 {
			err = errConfigurationBusy
		}
	} else {
		out.controllers, err = resolveFabricSSIDControllers(state.fabricID, members, snapshot.devices, snapshot.sites, true, status)
	}
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal([]any{path, out.siteID, out.fabricID, out.before, out.payload, out.members, out.physical, out.networks, out.profiles, out.profileContent, out.controllers})
	if err != nil {
		return nil, err
	}
	out.digest = fmt.Sprintf("%x", sha256.Sum256(raw))
	// Eligibility above covers every declaration; execution advances one owned
	// binding at a time while retaining the rest of the native replacement set.
	next, claim, err := selectNextFabricVLANSSID(want, before, owned)
	if err != nil {
		return nil, err
	}
	out.payload, out.claim = next.payload, claim
	return out, nil
}

// The existing shared resolver binds and rechecks every affected physical node.
func (c *client) vlanSSIDTargets(ctx context.Context, reader ctrlclient.Reader, namespace, path string, want []fabricVLANSSID, owned map[string]fabricVLANSSIDOwned) (*fabricVLANSSIDDependencies, []contract.GroupTargetIdentity, error) {
	return observeConfigurationManagedTargets(ctx, reader, namespace, func(ctx context.Context) (configurationPhysicalObservation[*fabricVLANSSIDDependencies], error) {
		state, err := c.vlanSSIDDependencies(ctx, path, want, owned)
		if err != nil {
			return configurationPhysicalObservation[*fabricVLANSSIDDependencies]{}, err
		}
		return configurationPhysicalObservation[*fabricVLANSSIDDependencies]{state: state, physical: state.physical, digest: state.digest}, nil
	})
}

// Preparation binds the prospective write to the same stable native and managed
// target observations. The returned closure cannot submit; dispatch must rebuild
// it with a permit from the durable group operation.
func (c *client) planManagedFabricVLANSSIDs(ctx context.Context, reader ctrlclient.Reader, namespace, path string, want []fabricVLANSSID, owned map[string]fabricVLANSSIDOwned) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	before, targets, err := c.vlanSSIDTargets(ctx, reader, namespace, path, want, owned)
	if err != nil {
		return nil, nil, err
	}
	write, err := c.planGuardedFabricVLANSSIDs(ctx, path, want, owned, func(context.Context) error { return errConfigurationDeviceAdmission })
	if err != nil {
		return nil, nil, err
	}
	resource, err := fabricVLANSSIDResource(path, before)
	if err != nil {
		return nil, nil, err
	}
	if write != nil && (write.resource != resource || write.identity.target != before.fabricID || write.identity.parent != before.siteID) {
		return nil, nil, errConfigurationStale
	}
	after, current, err := c.vlanSSIDTargets(ctx, reader, namespace, path, want, owned)
	if err != nil {
		return nil, nil, err
	}
	if before.digest != after.digest || !reflect.DeepEqual(targets, current) {
		return nil, nil, errConfigurationStale
	}
	return write, targets, nil
}
