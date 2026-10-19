// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"slices"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Only generated, disabled native qualification fixtures are eligible. This is
// test recovery evidence, never a product deletion/ownership authorization API.
type ssidQualificationIdentity struct {
	Name       string `json:"name"`
	SiteID     string `json:"siteId"`
	ID         string `json:"id"`
	Posts      int    `json:"posts"`
	Puts       int    `json:"puts"`
	Deletes    int    `json:"deletes"`
	MultiPSK   bool   `json:"multiPSK"`
	Retirement bool   `json:"retirement"`
}

func validateSSIDQualificationRecovery(identity ssidQualificationIdentity, state *cvk.NetworkControllerConfigurationStatus, source []byte) error {
	if state == nil || state.Operation == nil || len(state.CredentialApplications) != 1 {
		return errConfigurationTransition
	}
	op := state.Operation
	if !strings.HasPrefix(identity.Name, "CVK_PSK_") || !validProfileName(identity.Name) || !validAPIID(identity.ID) || !validAPIID(identity.SiteID) || identity.Posts != 1 || identity.Deletes != 0 || identity.Puts < 1 || identity.Puts > 4 || !identity.Retirement && identity.Puts != 1 || identity.Retirement && !identity.MultiPSK {
		return errConfigurationTransition
	}
	if op.Section != "wireless" || op.Action != "Update" || !slices.Contains([]string{"Accepted", "Verifying", "Unknown"}, op.Phase) || op.TargetID != identity.ID || op.ParentID != identity.SiteID || !configurationCredentialResourceSame("wireless", op.Resource, "ssid-credential:"+identity.SiteID+":"+identity.Name) || !validAPIID(op.TaskID) || op.SubmittedAt == nil || !configurationOperationID.MatchString(op.ID) || op.ConfigUID == "" || op.ConfigName == "" || op.ConfigGeneration < 1 || op.ControllerGeneration < 1 || op.TargetSerial != "" || op.CreatedResourceID != "" || op.PhysicalGroup != nil || op.DependencyVersions != nil || op.CancellationRequestedAt != nil {
		return errConfigurationTransition
	}
	prior := state.CredentialApplications[0]
	if prior.Section != "wireless" || prior.ConfigUID != op.ConfigUID || prior.TargetID != identity.ID || prior.OperationID == op.ID || !configurationOperationID.MatchString(prior.OperationID) || !configurationCredentialResourceSame("wireless", prior.Resource, op.Resource) {
		return errConfigurationOwned
	}
	rotate, history, err := wirelessSSIDCredentialTransition(&prior, op.CredentialVersions, true)
	if err != nil || !rotate {
		return errConfigurationSecret
	}
	if err := validateSSIDCredentialHistory(op.Section, op.Resource, op.CredentialVersions, op.SSIDCredentialHistory); err != nil {
		return err
	}
	if strings.HasPrefix(op.Resource, ssidHistoryResourcePrefix) && !slices.Equal(history, op.SSIDCredentialHistory) {
		return errConfigurationSecret
	}
	if identity.Retirement && (!strings.HasPrefix(op.Resource, ssidHistoryResourcePrefix) || identity.Puts > 1 && !strings.HasPrefix(prior.Resource, ssidHistoryResourcePrefix)) {
		return errConfigurationSecret
	}
	old, want := map[string]int64{"passphrase": 1}, map[string]int64{"passphrase": 2}
	if identity.MultiPSK {
		old = map[string]int64{"passphrase": 1, "multi-psk:0": 1, "multi-psk:4": 1}
		want = map[string]int64{"passphrase": 1, "multi-psk:0": 1, "multi-psk:4": 2}
		switch identity.Puts {
		case 2:
			old["multi-psk:4"] = 2
			delete(want, "multi-psk:4")
		case 3:
			delete(old, "multi-psk:4")
			want["multi-psk:4"] = 3
		case 4:
			old["multi-psk:4"] = 3
			delete(want, "multi-psk:0")
			delete(want, "multi-psk:4")
		}
	}
	check := func(versions []cvk.NetworkControllerConfigurationCredentialVersion, expected map[string]int64) bool {
		if len(versions) != len(expected) {
			return false
		}
		for _, v := range versions {
			if expected[v.Key] != v.Version {
				return false
			}
		}
		return true
	}
	if !check(prior.Versions, old) || !check(op.CredentialVersions, want) {
		return errConfigurationSecret
	}
	rows, err := parseWirelessSSIDs(source)
	if err != nil || len(rows) != 1 || rows[0].name != identity.Name || rows[0].metadata["isEnabled"] != false || rows[0].metadata["authType"] != "WPA2_PERSONAL" || len(rows[0].credentials) != len(op.CredentialVersions) {
		return errConfigurationStale
	}
	if identity.Retirement && identity.Puts > 1 {
		if _, owned := rows[0].metadata["multiPSKSettings"]; !owned {
			return errConfigurationSecret
		}
	}
	for _, v := range op.CredentialVersions {
		key, err := wirelessSSIDCredentialKey(&configSourceSnapshot{raw: source}, v.Path, v.Version)
		if err != nil || key != v.Key {
			return errConfigurationSecret
		}
	}
	return nil
}

func validateSSIDQualificationCleanup(identity ssidQualificationIdentity, operation *cvk.NetworkControllerConfigurationOperation, cleanup qualificationCleanup) error {
	if operation == nil || cleanup.Name != identity.Name || cleanup.ID != identity.ID || cleanup.UpdateTaskID != operation.TaskID || !cleanup.DeleteAttempted || cleanup.SubmittedAt.IsZero() || !validAPIID(cleanup.DeleteTaskID) {
		return errConfigurationBusy
	}
	return nil
}
