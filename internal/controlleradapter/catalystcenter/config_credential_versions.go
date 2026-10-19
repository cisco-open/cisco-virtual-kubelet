// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// A single projected Secret key carries {"version":N,"value":"..."}. This
// binds the counter to the bytes atomically across Kubernetes projection lag;
// a model counter plus a separately projected bare value cannot do that. The
// wrapper is a CVK Secret transport format, not part of the NetAsCode model.
func decodeConfigurationVersionedSecret(raw []byte, expected int64) (string, error) {
	if expected < 1 || len(raw) > 32<<10 {
		return "", errConfigurationSecret
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	start, err := d.Token()
	if err != nil || start != json.Delim('{') {
		return "", errConfigurationSecret
	}
	seen := map[string]bool{}
	var version int64
	var value string
	for d.More() {
		token, err := d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return "", errConfigurationSecret
		}
		seen[key] = true
		switch key {
		case "version":
			err = d.Decode(&version)
		case "value":
			err = d.Decode(&value)
		default:
			return "", errConfigurationSecret
		}
		if err != nil {
			return "", errConfigurationSecret
		}
	}
	end, err := d.Token()
	if err != nil || end != json.Delim('}') || len(seen) != 2 || version != expected || len(value) == 0 || len(value) > 4096 || strings.ContainsRune(value, 0) {
		return "", errConfigurationSecret
	}
	if _, err := d.Token(); err != io.EOF {
		return "", errConfigurationSecret
	}
	return value, nil
}

// Reserve enough status capacity before the remote mutation, not after it has
// succeeded. The controller-wide operation slot serializes receipt producers.
// Bound serialized size as well as count; long source references are allowed.
func configurationCredentialCapacity(state *ciskov1.NetworkControllerConfigurationStatus, owner, section, resource string, versions []ciskov1.NetworkControllerConfigurationCredentialVersion, histories ...[]ciskov1.NetworkControllerConfigurationSSIDCredentialHistory) error {
	var history []ciskov1.NetworkControllerConfigurationSSIDCredentialHistory
	if len(histories) > 1 {
		return errConfigurationTransition
	}
	if len(histories) == 1 {
		history = histories[0]
	}
	if err := validateSSIDCredentialHistory(section, resource, versions, history); err != nil {
		return err
	}
	if len(versions) == 0 {
		return nil
	}
	receipts := slices.Clone(state.CredentialApplications)
	prospective := ciskov1.NetworkControllerConfigurationCredentialApplication{
		ConfigUID: owner, Section: section, Resource: resource,
		TargetID: strings.Repeat("x", 128), OperationID: "00000000000000000000000000000000", Versions: versions, SSIDCredentialHistory: slices.Clone(history),
	}
	index := -1
	for i, receipt := range receipts {
		if receipt.Section == section && configurationCredentialResourceSame(section, receipt.Resource, resource) {
			if index != -1 || receipt.ConfigUID != owner {
				return errConfigurationOwned
			}
			index = i
		}
	}
	if index == -1 {
		receipts = append(receipts, prospective)
	} else {
		// Compare the proposed history with the latest durable receipt as well as
		// the compiled plan. A stale planner must not lower a retired counter.
		if len(history) > 0 {
			_, expected, err := wirelessSSIDCredentialTransition(&receipts[index], versions, true)
			if err != nil || !slices.Equal(history, expected) {
				return errConfigurationSecret
			}
		}
		receipts[index] = prospective
	}
	encoded, err := json.Marshal(receipts)
	if err != nil || len(receipts) > 256 || len(encoded) > 256<<10 {
		return errConfigurationTransition
	}
	return nil
}

// Each operation binds only its own credential leaves, not every credential
// elsewhere in the same source model. Require canonical ordering so a pruned,
// duplicated or reordered persisted binding cannot pass a submission check.
func validateConfigurationCredentialVersions(source *configSourceSnapshot, section string, versions []ciskov1.NetworkControllerConfigurationCredentialVersion) error {
	if len(versions) == 0 {
		return nil
	}
	if len(versions) > 128 || source == nil || source.secrets == nil {
		return errConfigurationSecret
	}
	previous := ""
	semanticKeys := map[string]bool{}
	for _, version := range versions {
		if section == "wireless" && strings.HasPrefix(version.Path, "/ssids/") {
			key, err := wirelessSSIDCredentialKey(source, version.Path, version.Version)
			if err != nil || version.Key != key || semanticKeys[key] {
				return errConfigurationSecret
			}
			semanticKeys[key] = true
		} else if version.Key != "" {
			return errConfigurationSecret
		}
		if version.Version < 1 || version.Path <= previous {
			return errConfigurationSecret
		}
		previous = version.Path
		found := false
		for _, value := range source.secrets.values {
			if value.ref.Section == section && value.ref.Path == version.Path && value.ref.Source == version.SourceAlias &&
				value.source.Name == version.SecretName && value.source.Key == version.SecretKey {
				if _, err := decodeConfigurationVersionedSecret(value.value, version.Version); err != nil {
					return err
				}
				found = true
				break
			}
		}
		if !found {
			return errConfigurationSecret
		}
	}
	return nil
}

// recordConfigurationCredentialApplication is called only after resource
// verification supplies an observed UUID. Recheck the correlated successful
// task: Unknown -> Verifying recovery or metadata equality is insufficient to
// assert a write-only credential was applied. The receipt and Verified phase
// are persisted in one CAS; a lost acknowledgement must never cause a replay.
func (a *adapter) recordConfigurationCredentialApplication(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, operationID, observedID string) error {
	if !configurationOperationID.MatchString(operationID) || !validAPIID(observedID) {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	state, op := nc.Status.Configuration, nc.Status.Configuration.Operation
	// Discovery creation also needs a creator UUID receipt in this same CAS.
	if discoveryCredentialInputSelected(op.Resource) || discoveryCredentialRetirementSelected(op.Resource) {
		return errConfigurationTransition
	}
	if op.ID != operationID || op.Phase != "Verifying" || op.SubmittedAt == nil || !validAPIID(op.TaskID) ||
		op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation ||
		op.ControllerGeneration != nc.Generation || source == nil || op.SourceUID != string(source.uid) || op.SourceVersion != source.version ||
		(op.TargetID != "" && op.TargetID != observedID) || len(op.CredentialVersions) == 0 {
		return errConfigurationTransition
	}
	if err := validateConfigurationCredentialVersions(source, op.Section, op.CredentialVersions); err != nil {
		return err
	}
	if err := configurationCredentialCapacity(state, op.ConfigUID, op.Section, op.Resource, op.CredentialVersions, op.SSIDCredentialHistory); err != nil {
		return err
	}
	owner := false
	for _, entry := range state.Owners {
		if entry.Section == op.Section && entry.ConfigUID == op.ConfigUID && entry.ConfigName == op.ConfigName {
			owner = true
		}
	}
	if !owner {
		return errConfigurationOwned
	}
	if err := a.checkGlobalCredentialPromotionCommit(ctx, config, source, nc, observedID); err != nil {
		return err
	}
	index := -1
	for i, receipt := range state.CredentialApplications {
		if receipt.Section == op.Section && configurationCredentialResourceSame(op.Section, receipt.Resource, op.Resource) {
			if index != -1 || receipt.ConfigUID != op.ConfigUID {
				return errConfigurationOwned
			}
			index = i
		}
	}
	if index == -1 && len(state.CredentialApplications) >= 256 {
		return errConfigurationTransition
	}
	var done bool
	if isGlobalCredentialResource(op.Section, op.Resource) {
		if !globalCredentialTaskEnvelope(op) {
			return errConfigurationTransition
		}
		done, err = a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	} else {
		done, err = a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, time.Now())
	}
	if err != nil {
		return err
	}
	if !done {
		return errConfigurationBusy
	}
	receipt := ciskov1.NetworkControllerConfigurationCredentialApplication{
		Section: op.Section, Resource: op.Resource, ConfigUID: op.ConfigUID,
		TargetID: observedID, OperationID: op.ID, Versions: slices.Clone(op.CredentialVersions), SSIDCredentialHistory: slices.Clone(op.SSIDCredentialHistory),
	}
	if index == -1 {
		state.CredentialApplications = append(state.CredentialApplications, receipt)
	} else {
		state.CredentialApplications[index] = receipt
	}
	op.Phase = "Verified"
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		return err
	}
	stored, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if stored.Status.Configuration == nil || stored.Status.Configuration.Operation == nil {
		return errConfigurationStale
	}
	persisted := stored.Status.Configuration.Operation
	if persisted.ID != operationID || persisted.Phase != "Verified" || !slices.Equal(persisted.CredentialVersions, receipt.Versions) || !slices.Equal(persisted.SSIDCredentialHistory, receipt.SSIDCredentialHistory) {
		return errConfigurationStale
	}
	for _, entry := range stored.Status.Configuration.CredentialApplications {
		if entry.Section == receipt.Section && entry.Resource == receipt.Resource && entry.ConfigUID == receipt.ConfigUID && entry.TargetID == receipt.TargetID && entry.OperationID == receipt.OperationID && slices.Equal(entry.Versions, receipt.Versions) && slices.Equal(entry.SSIDCredentialHistory, receipt.SSIDCredentialHistory) {
			return nil
		}
	}
	return errConfigurationStale
}
