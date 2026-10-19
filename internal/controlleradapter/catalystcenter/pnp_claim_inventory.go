// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"sort"
	"strings"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

// Independent post-claim evidence, not completion. A network UUID can differ
// from the PnP UUID. Nil inventory or false assigned records pending observation;
// neither the PnP state string nor inventory existence releases a claim fence.
type pnpClaimInventorySnapshot struct {
	pnp       pnpDevice
	site      Site
	inventory *Device
	assigned  bool
}

func bindPnPClaimNetworkDevice(serials []string, inventory []Device) (*Device, error) {
	wanted := map[string]bool{}
	for _, s := range serials {
		serial, err := topologyidentity.CanonicalPhysicalIdentity(s)
		if err != nil || wanted[serial] {
			return nil, errInvalidResponse
		}
		wanted[serial] = true
	}
	if len(wanted) == 0 {
		return nil, errInvalidResponse
	}
	var selected *Device
	ids := map[string]bool{}
	for _, device := range inventory {
		if !validAPIID(device.ID) || ids[device.ID] {
			return nil, errInvalidResponse
		}
		ids[device.ID] = true
		members := map[string]bool{}
		matches := 0
		valid := true
		for _, s := range strings.Split(device.Serial, ",") {
			serial, err := topologyidentity.CanonicalPhysicalIdentity(s)
			if err != nil || members[serial] {
				valid = false
				continue
			}
			members[serial] = true
			if wanted[serial] {
				matches++
			}
		}
		if matches == 0 {
			continue
		}
		if !valid || selected != nil || matches != len(wanted) || len(members) != len(wanted) {
			return nil, errConfigurationStale
		}
		copy := device
		// Normalize only set ordering/case so inventory serialization does not
		// look like physical replacement during repeated observations.
		canonical := make([]string, 0, len(members))
		for serial := range members {
			canonical = append(canonical, serial)
		}
		sort.Strings(canonical)
		copy.Serial = strings.Join(canonical, ",")
		selected = &copy
	}
	return selected, nil
}

func (c *client) pnpClaimInventorySnapshot(ctx context.Context, want pnpDeviceIntent, pnpID, siteID string) (*pnpClaimInventorySnapshot, error) {
	serials, err := validatePnPDeviceIntent(want)
	if err != nil {
		return nil, err
	}
	if !validAPIID(pnpID) || !validAPIID(siteID) {
		return nil, errInvalidResponse
	}
	rows, err := c.pnpDevices(ctx)
	if err != nil {
		return nil, err
	}
	selected, err := bindPnPPhysicalDevices(serials, rows, nil)
	if err != nil {
		return nil, err
	}
	if len(selected) != 1 || !pnpRegistrationMatches(pnpRegistrationReceipt{deviceID: pnpID, serial: serials[0], pid: want.PID, hostname: want.Name, stack: len(serials) > 1}, selected[0]) {
		return nil, errConfigurationStale
	}
	result := &pnpClaimInventorySnapshot{pnp: selected[0]}
	observedSite := result.pnp.Info.SiteID
	if observedSite == "" {
		observedSite = result.pnp.Info.Location.SiteID
	}
	if observedSite != "" && observedSite != siteID {
		return nil, errConfigurationStale
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	for _, site := range sites {
		if site.NameHierarchy == want.Site {
			result.site = site
		}
	}
	if result.site.ID != siteID {
		return nil, errConfigurationStale
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	result.inventory, err = bindPnPClaimNetworkDevice(serials, inventory)
	if err != nil {
		return nil, err
	}
	if result.inventory == nil {
		return result, nil
	}
	assigned, err := c.assignedDevices(ctx, siteID)
	if err != nil {
		return nil, err
	}
	result.assigned = assigned[result.inventory.ID]
	return result, nil
}

func (c *client) revalidatePnPClaimInventory(ctx context.Context, want pnpDeviceIntent, pnpID, siteID string, previous *pnpClaimInventorySnapshot) error {
	if previous == nil {
		return errConfigurationStale
	}
	current, err := c.pnpClaimInventorySnapshot(ctx, want, pnpID, siteID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(previous, current) {
		return errConfigurationStale
	}
	return nil
}
