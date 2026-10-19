// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

// This is observation, not ownership or permission to start replacement. In
// particular, an absent replacement in network inventory may still be in PnP.
// A dead faulty device need not be Reachable to bind its retained hardware ID.
type deviceReplacementSnapshot struct {
	siteID, provisionID string
	target              Device
	serial              string
	replacement         *Device
	replacementSerial   string
	record              *deviceReplacementRecord
}

func bindDeviceReplacement(ref configurationDeviceReference, want deviceReplacementIntent, inventory []Device, records []deviceReplacementRecord) (*deviceReplacementSnapshot, error) {
	if want.action != rmaMark && want.action != rmaReplace {
		return nil, errInvalidResponse
	}
	if want.action == rmaReplace && (ref.Serial == "" || want.replacementSerial == "") {
		return nil, errInvalidResponse
	}
	target, err := resolveConfigurationDevice(ref, inventory)
	if err != nil {
		return nil, err
	}
	bound, err := bindMulticastRPDevices([]string{target.ID}, inventory)
	if err != nil {
		return nil, err
	}
	result := &deviceReplacementSnapshot{target: target, serial: bound[0].Serial}
	if want.replacementSerial != "" {
		result.replacementSerial, err = topologyidentity.CanonicalPhysicalIdentity(want.replacementSerial)
		if err != nil || result.replacementSerial == result.serial {
			return nil, errInvalidResponse
		}
		for _, device := range inventory {
			serial, err := topologyidentity.CanonicalPhysicalIdentity(device.Serial)
			if err != nil || serial != result.replacementSerial {
				continue
			}
			if result.replacement != nil {
				return nil, errConfigurationStale
			}
			copy := device
			result.replacement = &copy
		}
	}
	for _, record := range records {
		if !validAPIID(record.ID) || !validAPIID(record.FaultyID) {
			return nil, errInvalidResponse
		}
		faulty, replacement := "", ""
		if record.FaultySerial != "" {
			faulty, err = topologyidentity.CanonicalPhysicalIdentity(record.FaultySerial)
			if err != nil {
				return nil, errInvalidResponse
			}
		}
		if record.ReplacementSerial != "" {
			replacement, err = topologyidentity.CanonicalPhysicalIdentity(record.ReplacementSerial)
			if err != nil {
				return nil, errInvalidResponse
			}
		}
		if record.FaultyID == target.ID {
			if result.record != nil || faulty != "" && faulty != result.serial {
				return nil, errConfigurationStale
			}
			if replacement == result.serial || replacement != "" && result.replacementSerial != "" && replacement != result.replacementSerial {
				return nil, errConfigurationOwned
			}
			copy := record
			result.record = &copy
			continue
		}
		// The selected hardware must not participate in another replacement record,
		// even when that record is found under a different native device UUID.
		if faulty == result.serial || replacement == result.serial || result.replacementSerial != "" && (faulty == result.replacementSerial || replacement == result.replacementSerial) {
			return nil, errConfigurationOwned
		}
	}
	return result, nil
}

func (c *client) deviceReplacementSnapshot(ctx context.Context, ref configurationDeviceReference, want deviceReplacementIntent) (*deviceReplacementSnapshot, error) {
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	records, err := c.deviceReplacements(ctx)
	if err != nil {
		return nil, err
	}
	snapshot, err := bindDeviceReplacement(ref, want, inventory, records)
	if err != nil {
		return nil, err
	}
	if want.site != "" {
		if err := c.bindReplacementProvisioning(ctx, want.site, snapshot); err != nil {
			return nil, err
		}
	}
	return snapshot, nil
}

// An unchanged native snapshot is a prerequisite, never a substitute for the
// manager/device-worker mutation reservation and owned journal receipt.
func (c *client) revalidateDeviceReplacement(ctx context.Context, ref configurationDeviceReference, want deviceReplacementIntent, before *deviceReplacementSnapshot) error {
	if before == nil {
		return errConfigurationTransition
	}
	after, err := c.deviceReplacementSnapshot(ctx, ref, want)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(before, after) {
		return errConfigurationStale
	}
	return nil
}

// PROVISION in an RMA model describes retained provisioning. Do not submit a
// fresh provisioning action to a faulty device just to satisfy RMA prerequisites.
func (c *client) bindReplacementProvisioning(ctx context.Context, path string, snapshot *deviceReplacementSnapshot) error {
	if snapshot == nil || !validSitePath(path) || path == "Global" {
		return errInvalidResponse
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return err
	}
	matches := 0
	for _, site := range sites {
		if site.NameHierarchy != path {
			continue
		}
		if !validAPIID(site.ID) || site.Type == "global" {
			return errInvalidResponse
		}
		matches++
		snapshot.siteID = site.ID
	}
	if matches != 1 {
		return errConfigurationStale
	}
	assigned, err := c.assignedDevices(ctx, snapshot.siteID)
	if err != nil {
		return err
	}
	if !assigned[snapshot.target.ID] {
		return errConfigurationBusy
	}
	provisioned, exists, err := c.wiredProvisionedDevice(ctx, snapshot.target.ID)
	if err != nil {
		return err
	}
	if !exists || provisioned.SiteID != snapshot.siteID {
		return errConfigurationStale
	}
	snapshot.provisionID = provisioned.ID
	return nil
}
