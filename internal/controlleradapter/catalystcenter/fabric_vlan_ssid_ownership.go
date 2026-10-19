// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func fabricVLANSSIDApplicationKey(resource string) (string, error) {
	parts, err := parseFabricVLANSSIDResource(resource)
	if err != nil {
		return "", err
	}
	return "fabric-vlan-ssid:" + parts[1] + ":" + parts[4], nil
}

// Read only receipts from the current controller's authoritative status. The
// caller must independently establish current section ownership by configUID.
// Generation/source changes do not erase durable ownership by that same UID.
// Matching native rows alone never establish ownership.
func fabricVLANSSIDOwnership(path, siteID, fabricID, configUID string, observed []fabricVLANSSID, applications []cvk.NetworkControllerConfigurationActionApplication) (map[string]fabricVLANSSIDOwned, error) {
	if !validSitePath(path) || path == "Global" || !validAPIID(siteID) || !validAPIID(fabricID) || configUID == "" {
		return nil, errConfigurationTransition
	}
	rows, err := canonicalFabricVLANSSIDs(observed)
	if err != nil {
		return nil, err
	}
	scope, _ := json.Marshal([]string{path, siteID, fabricID})
	prefix := fmt.Sprintf("fabric-vlan-ssid:%x:", sha256.Sum256(scope))
	bindings := map[string]fabricVLANSSID{}
	for _, row := range rows {
		for _, ssid := range row.SSIDs {
			bindings[fmt.Sprintf("%x", sha256.Sum256([]byte(ssid.Name)))] = fabricVLANSSID{VLAN: row.VLAN, SSIDs: []fabricSSIDBinding{ssid}}
		}
	}
	out := map[string]fabricVLANSSIDOwned{}
	seen := map[string]bool{}
	for _, application := range applications {
		if !strings.HasPrefix(application.Resource, prefix) {
			continue
		}
		op := &application.Operation
		key, err := fabricVLANSSIDApplicationKey(op.Resource)
		if err != nil || application.Resource != key || application.Section != "fabric" || op.Section != "fabric" || op.ConfigUID != configUID || op.TargetID != fabricID || op.ParentID != siteID || op.Action != "Update" || op.Phase != "Verified" || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || application.VerifiedAt == nil || application.VerifiedAt.IsZero() || application.VerifiedAt.Before(op.SubmittedAt) || op.CancellationRequestedAt != nil || contract.ValidateGroupJournalIdentity(op) != nil {
			return nil, errConfigurationStale
		}
		if seen[key] {
			return nil, errConfigurationStale
		}
		seen[key] = true
		parts, _ := parseFabricVLANSSIDResource(op.Resource)
		binding, exists := bindings[parts[4]]
		if !exists {
			continue
		} // Deleted rows are not adopted or recreated as owned.
		raw, _ := json.Marshal(binding)
		if fmt.Sprintf("%x", sha256.Sum256(raw)) != parts[5] {
			return nil, errConfigurationStale
		}
		ssid := binding.SSIDs[0]
		out[ssid.Name] = fabricVLANSSIDOwned{VLAN: binding.VLAN, SecurityGroup: ssid.SecurityGroup}
	}
	return out, nil
}

// During recovery only, the active operation may supersede the old receipt for
// its own binding. Require its exact full native result before excluding that
// receipt; all other receipts remain subject to ordinary ownership checks.
// This does not adopt the new binding: only atomic completion can do that.
func fabricVLANSSIDRecoveryOwnership(path, siteID, fabricID, configUID string, observed []fabricVLANSSID, applications []cvk.NetworkControllerConfigurationActionApplication, op *cvk.NetworkControllerConfigurationOperation) (map[string]fabricVLANSSIDOwned, error) {
	if op == nil || (op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying") || op.Section != "fabric" || op.Action != "Update" || op.ConfigUID != configUID || op.ParentID != siteID || op.TargetID != fabricID || !validAPIID(op.TaskID) || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.CancellationRequestedAt != nil || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationTransition
	}
	parts, err := parseFabricVLANSSIDResource(op.Resource)
	if err != nil {
		return nil, err
	}
	rows, err := canonicalFabricVLANSSIDs(observed)
	if err != nil {
		return nil, err
	}
	resource, err := fabricVLANSSIDResource(path, &fabricVLANSSIDDependencies{siteID: siteID, fabricID: fabricID, before: rows, payload: rows})
	if err != nil {
		return nil, err
	}
	actual := strings.Split(resource, ":")
	if actual[1] != parts[1] {
		return nil, errConfigurationStale
	}
	if actual[3] != parts[3] {
		return nil, errConfigurationBusy
	}
	found := false
	for _, row := range rows {
		for _, ssid := range row.SSIDs {
			if fmt.Sprintf("%x", sha256.Sum256([]byte(ssid.Name))) != parts[4] {
				continue
			}
			raw, _ := json.Marshal(fabricVLANSSID{VLAN: row.VLAN, SSIDs: []fabricSSIDBinding{ssid}})
			if fmt.Sprintf("%x", sha256.Sum256(raw)) != parts[5] {
				return nil, errConfigurationStale
			}
			found = true
		}
	}
	if !found {
		return nil, errConfigurationStale
	}
	// Validate all relevant receipts' provenance even if one is superseded. An
	// empty observation validates metadata without relying on obsolete bindings.
	if _, err := fabricVLANSSIDOwnership(path, siteID, fabricID, configUID, nil, applications); err != nil {
		return nil, err
	}
	key, _ := fabricVLANSSIDApplicationKey(op.Resource)
	retained := make([]cvk.NetworkControllerConfigurationActionApplication, 0, len(applications))
	for _, application := range applications {
		if application.Resource != key {
			retained = append(retained, application)
		}
	}
	return fabricVLANSSIDOwnership(path, siteID, fabricID, configUID, rows, retained)
}
