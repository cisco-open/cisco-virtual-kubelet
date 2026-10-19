// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Resolve the durable name digest against the already revision-checked source.
// Recovery consumes the recorded baseline/model requirement, never a new plan.
func wirelessProvisionRecoveryName(raw []byte, op *cvk.NetworkControllerConfigurationOperation) (string, error) {
	if op == nil || op.Section != "inventory" || op.Phase != "Verifying" {
		return "", errConfigurationTransition
	}
	record, err := decodeWirelessProvisionRecord(op.Resource)
	if err != nil {
		return "", err
	}
	intent, err := parseInventoryIntent(raw)
	if err != nil {
		return "", err
	}
	name := ""
	for _, d := range intent.devices {
		key, err := wirelessTagDigest(d.reference.Name)
		if err != nil {
			return "", err
		}
		if key != record.NameKey {
			continue
		}
		if name != "" || d.wireless == nil || d.wireless.deviceType != "WirelessController" || d.reference.Serial != record.Serial || (d.reference.IP != "" && !sameManagementIP(d.reference.IP, record.Address)) {
			return "", errConfigurationStale
		}
		for _, role := range d.wireless.roles {
			if role == "EMBEDDED_WIRELESS_CONTROLLER_NODE" {
				return "", errConfigurationStale
			}
		}
		action := "Create"
		if d.wireless.state == "REPROVISION" {
			action = "Update"
		}
		if action != record.Action {
			return "", errConfigurationStale
		}
		if _, err := restoreWirelessProvisionBaseline(d.reference.Name, op); err != nil {
			return "", err
		}
		name = d.reference.Name
	}
	if name == "" {
		return "", errConfigurationStale
	}
	return name, nil
}

// Verify dependencies encoded by the admitted compiler before retaining a
// successful application receipt. Legacy native records have no such digest.
func (c *client) verifyWirelessProvisionDependencies(ctx context.Context, raw []byte, op *cvk.NetworkControllerConfigurationOperation) error {
	if op == nil {
		return errConfigurationStale
	}
	record, err := decodeWirelessProvisionRecord(op.Resource)
	if err != nil {
		return err
	}
	if record.DependencyDigest == "" {
		return nil
	}
	intent, err := parseInventoryIntent(raw)
	if err != nil {
		return err
	}
	bindings, err := c.validateStandaloneWirelessInventory(ctx, intent.devices)
	if err != nil {
		return err
	}
	digest, err := wirelessTagDigest(bindings)
	if err != nil {
		return err
	}
	if digest != record.DependencyDigest {
		return errConfigurationStale
	}
	return nil
}
