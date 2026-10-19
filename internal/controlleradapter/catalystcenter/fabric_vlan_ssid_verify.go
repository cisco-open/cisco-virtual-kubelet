// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/hex"
	"reflect"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func parseFabricVLANSSIDResource(resource string) ([]string, error) {
	parts := strings.Split(resource, ":")
	if len(parts) != 6 || parts[0] != "fabric-vlan-ssid-update" {
		return nil, errConfigurationTransition
	}
	for _, part := range parts[1:] {
		raw, err := hex.DecodeString(part)
		if err != nil || len(raw) != 32 || hex.EncodeToString(raw) != part {
			return nil, errConfigurationTransition
		}
	}
	if parts[2] == parts[3] {
		return nil, errConfigurationTransition
	}
	return parts, nil
}

// Verify the entire replacement result, including undeclared retained mappings
// and tags. This is native readback only: task, source and group authority must
// be checked independently before advancing or releasing the shared journal.
func (c *client) verifyFabricVLANSSIDs(ctx context.Context, path string, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Section != "fabric" || op.Phase != "Verifying" || op.Action != "Update" || !validAPIID(op.TaskID) || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) || op.TargetSerial != "" || op.CreatedResourceID != "" || len(op.CredentialVersions) != 0 || op.CancellationRequestedAt != nil {
		return false, errConfigurationTransition
	}
	expected, err := parseFabricVLANSSIDResource(op.Resource)
	if err != nil {
		return false, err
	}
	before, err := c.observeFabricVLANSSIDs(ctx, path)
	if err != nil {
		return false, err
	}
	after, err := c.observeFabricVLANSSIDs(ctx, path)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(before, after) {
		return false, errConfigurationStale
	}
	if before.siteID != op.ParentID || before.fabricID != op.TargetID {
		return false, errConfigurationStale
	}
	// Hash observed state into the result slot; the old baseline is deliberately
	// not reconstructed or treated as evidence of ownership.
	resource, err := fabricVLANSSIDResource(path, &fabricVLANSSIDDependencies{siteID: before.siteID, fabricID: before.fabricID, before: before.mappings, payload: before.mappings})
	if err != nil {
		return false, err
	}
	actual := strings.Split(resource, ":")
	if actual[1] != expected[1] {
		return false, errConfigurationStale
	}
	return actual[3] == expected[3], nil
}
