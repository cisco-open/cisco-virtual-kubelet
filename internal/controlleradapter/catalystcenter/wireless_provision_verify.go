// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"strconv"
	"time"
)

// This baseline must be durably bound before submission. It is not yet part of
// manifest execution; losing it cannot authorize a successful recovery.
// Compare appliance epochs to each other without guessing their time unit.
type wirelessProvisionBaseline struct {
	device                            Device
	resource, siteID, lastProvisioned string
	modelConfigRequired               bool
}

func (c *client) verifyWirelessProvision(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, b wirelessProvisionBaseline, now time.Time) (bool, error) {
	if op == nil || op.Phase != "Verifying" || op.SubmittedAt == nil || op.Action != "Create" && op.Action != "Update" || op.ParentID != "" || len(op.CredentialVersions) > 0 || !validAPIID(b.device.ID) || b.device.Serial == "" || !validAPIID(b.siteID) || b.resource == "" || op.Resource != b.resource || op.TargetID != b.device.ID || op.TargetSerial != b.device.Serial {
		return false, errConfigurationTransition
	}
	done, err := c.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		return false, err
	}
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		return false, err
	}
	device, err := resolveConfigurationDevice(configurationDeviceReference{Serial: b.device.Serial, IP: b.device.ManagementIP}, inventory)
	if err != nil {
		return false, err
	}
	if device.ID != b.device.ID || device.Serial != b.device.Serial || device.Family != "Wireless Controller" || device.Reachability != "Reachable" {
		return false, errConfigurationStale
	}
	status, err := c.wirelessDeviceProvisionStatus(ctx, device.ID)
	if err != nil {
		return false, err
	}
	if status.siteID != b.siteID || status.name != device.Hostname {
		return false, errConfigurationStale
	}
	for _, state := range []string{status.networkIntent, status.modelConfig} {
		switch state {
		case "FAILED", "ROLLBACK_SUCCESS", "ROLLBACK_FAILURE", "ABORTED":
			return false, errors.New("wireless provisioning did not converge successfully")
		}
	}
	if status.outOfSync || status.networkIntent != "SUCCESS" || status.modelConfig != "SUCCESS" && (b.modelConfigRequired || status.modelConfig != "NOT_STARTED") {
		return false, nil
	}
	if status.lastProvisioned == "" {
		return false, nil
	}
	after, err := strconv.ParseUint(status.lastProvisioned, 10, 64)
	if err != nil || after == 0 {
		return false, errInvalidResponse
	}
	if b.lastProvisioned != "" {
		before, err := strconv.ParseUint(b.lastProvisioned, 10, 64)
		if err != nil || before == 0 || strconv.FormatUint(before, 10) != b.lastProvisioned {
			return false, errInvalidResponse
		}
		if after <= before {
			return false, nil
		}
	}
	return true, nil
}
