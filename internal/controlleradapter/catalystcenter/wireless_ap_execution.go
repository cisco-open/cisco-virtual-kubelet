// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type wirelessAPExecutor struct {
	client   *client
	desired  []wirelessAPIntent
	secrets  *configurationSecretSnapshot
	owner    string
	receipts []ciskov1.NetworkControllerConfigurationCredentialApplication
}

var errWirelessAPImmutable = errors.New("AP remote-worker mode is create-only; use a new profile name and retain the existing profile")

func (e *wirelessAPExecutor) count() int { return len(e.desired) }
func (want wirelessAPIntent) resource() string {
	if len(want.credentials) > 0 {
		return "ap-credential-profile:" + want.name
	}
	return "ap-profile:" + want.name
}
func isWirelessAPCredentialResource(section, resource string) bool {
	return section == "wireless" && strings.HasPrefix(resource, "ap-credential-profile:")
}

func wirelessAPVersionIdentity(path string) string {
	p, err := configurationSecretPointer(path)
	if err != nil || len(p) != 3 || p[0] != "ap_profiles" {
		return ""
	}
	index, err := strconv.Atoi(p[1])
	if err != nil || index < 0 || strconv.Itoa(index) != p[1] {
		return ""
	}
	if _, ok := wirelessAPPasswords[p[2]]; !ok {
		return ""
	}
	return p[2]
}
func wirelessAPPublicMatches(want, actual map[string]any) bool {
	for key, value := range want {
		got, exists := actual[key]
		if !exists {
			return false
		}
		if nested, ok := value.(map[string]any); ok {
			other, ok := got.(map[string]any)
			if !ok || !wirelessAPPublicMatches(nested, other) {
				return false
			}
		} else if !reflect.DeepEqual(value, got) {
			return false
		}
	}
	return true
}
func cloneWirelessAPFields(fields map[string]any) map[string]any {
	out := make(map[string]any, len(fields))
	for key, value := range fields {
		if child, ok := value.(map[string]any); ok {
			out[key] = cloneWirelessAPFields(child)
		} else {
			out[key] = value
		}
	}
	return out
}
func mergeWirelessAPFields(base, desired map[string]any) map[string]any {
	out := cloneWirelessAPFields(base)
	for key, value := range desired {
		if child, ok := value.(map[string]any); ok {
			old, _ := out[key].(map[string]any)
			out[key] = mergeWirelessAPFields(old, child)
		} else {
			out[key] = value
		}
	}
	return out
}
func (want wirelessAPIntent) payload(snapshot *configurationSecretSnapshot, owner string, actual map[string]any) ([]byte, []ciskov1.NetworkControllerConfigurationCredentialVersion, error) {
	payload := mergeWirelessAPFields(actual, want.metadata)
	// Observation-only sentinels must never be submitted as explicit settings.
	if payload["countryCode"] == "unconfigured" {
		delete(payload, "countryCode")
	}
	if management, ok := payload["managementSetting"].(map[string]any); ok {
		for _, key := range []string{"dot1xUsername", "managementUserName"} {
			if management[key] == "" {
				delete(management, key)
			}
		}
	}
	// Empty version lists are omitted by the Kubernetes JSON journal. Keep
	// the planner identity canonical so its exact persisted comparison agrees.
	var versions []ciskov1.NetworkControllerConfigurationCredentialVersion
	if len(want.credentials) > 0 {
		values, bound, err := resolveVersionedConfigurationCredentials(snapshot, owner, "wireless", want.credentials)
		if err != nil {
			return nil, nil, err
		}
		versions = bound
		for path, value := range values {
			if len(value) < 8 || len(value) > 120 || strings.ContainsAny(value, "\r\n") {
				return nil, nil, errConfigurationSecret
			}
			setWirelessAPField(payload, path, value)
		}
	}
	if err := validateWirelessAPPayload(payload); err != nil {
		return nil, nil, err
	}
	raw, err := json.Marshal(payload)
	return raw, versions, err
}
func (e *wirelessAPExecutor) dependencySnapshot(ctx context.Context, planned map[string]bool, observed map[string]wirelessAPMetadata) (map[string]wirelessPowerProfile, error) {
	required := map[string]bool{}
	for _, want := range e.desired {
		// The update payload preserves native fields omitted by the model.
		// Protect those retained references as well as explicit references.
		fields := mergeWirelessAPFields(observed[want.name].fields, want.metadata)
		if name, ok := fields["apPowerProfileName"].(string); ok && name != "" {
			required[name] = true
		}
		if calendar, ok := fields["calendarPowerProfiles"].([]any); ok {
			for _, v := range calendar {
				entry := v.(map[string]any)
				name, ok := entry["powerProfileName"].(string)
				if !ok || name == "" {
					return nil, errors.New("AP calendar requires a power profile")
				}
				required[name] = true
			}
		}
	}
	if len(required) == 0 {
		return map[string]wirelessPowerProfile{}, nil
	}
	actual, err := e.client.wirelessPowerProfiles(ctx)
	if err != nil {
		return nil, err
	}
	snapshot := map[string]wirelessPowerProfile{}
	for name := range required {
		if _, ok := actual[name]; !ok && !planned[name] {
			return nil, errors.New("AP power profile dependency is unavailable")
		}
		if profile, ok := actual[name]; ok {
			snapshot[name] = profile
		}
	}
	return snapshot, nil
}
func protectedWirelessAP(name string) bool {
	return strings.EqualFold(name, "default-ap-profile") || strings.EqualFold(name, "Default_AP_Profile_AireOS")
}
func (e *wirelessAPExecutor) next(ctx context.Context) (*configurationWrite, error) {
	return e.plan(ctx, nil)
}
func (e *wirelessAPExecutor) plan(ctx context.Context, planned map[string]bool) (*configurationWrite, error) {
	actual, err := e.client.wirelessAPMetadataInventory(ctx)
	if err != nil {
		return nil, err
	}
	dependencies, err := e.dependencySnapshot(ctx, planned, actual)
	if err != nil {
		return nil, err
	}
	var first *configurationWrite
	for _, want := range e.desired {
		got, exists := actual[want.name]
		if exists {
			mode, ok := got.fields["remoteWorkerEnabled"].(bool)
			desiredMode, desiredOK := want.metadata["remoteWorkerEnabled"].(bool)
			if !ok || !desiredOK {
				return nil, errWirelessAPModel
			}
			if mode != desiredMode {
				return nil, errWirelessAPImmutable
			}
		}
		var prior *ciskov1.NetworkControllerConfigurationCredentialApplication
		for i := range e.receipts {
			r := &e.receipts[i]
			if r.Section == "wireless" && (r.Resource == "ap-profile:"+want.name || r.Resource == "ap-credential-profile:"+want.name) {
				if prior != nil || r.ConfigUID != e.owner || !validAPIID(r.TargetID) || !configurationOperationID.MatchString(r.OperationID) || len(r.Versions) == 0 {
					return nil, errConfigurationOwned
				}
				prior = r
			}
		}
		if prior != nil && (len(want.credentials) == 0 || prior.Resource != want.resource()) {
			return nil, errConfigurationSecret
		}
		if len(want.credentials) > 0 && exists && (prior == nil || prior.TargetID != got.id) {
			return nil, errConfigurationOwned
		}
		if exists && len(want.credentials) == 0 && wirelessAPPublicMatches(want.metadata, got.fields) {
			continue
		}
		body, versions, err := want.payload(e.secrets, e.owner, got.fields)
		if err != nil {
			return nil, err
		}
		rotate := len(versions) > 0
		if prior != nil {
			rotate, err = configurationCredentialRotation(prior.Versions, versions, wirelessAPVersionIdentity)
			if err != nil {
				return nil, err
			}
		}
		if exists && !rotate && wirelessAPPublicMatches(want.metadata, got.fields) {
			continue
		}
		if protectedWirelessAP(want.name) {
			return nil, errors.New("built-in AP profiles cannot be modified")
		}
		// Metadata-only requests may not rewrite credential-bearing management
		// settings without the durable ownership and version bindings.
		if exists && len(versions) == 0 {
			auth, _ := getWirelessAPField(got.fields, "managementSetting.authType")
			ssh, _ := getWirelessAPField(got.fields, "managementSetting.sshEnabled")
			telnet, _ := getWirelessAPField(got.fields, "managementSetting.telnetEnabled")
			if auth != "NO-AUTH" || ssh == true || telnet == true {
				return nil, errConfigurationSecret
			}
		}
		method, path, action := http.MethodPost, wirelessAPPath, "Create"
		identity := configurationResourceIdentity{credentials: versions}
		if exists {
			method, path, action = http.MethodPut, wirelessAPPath+"/"+url.PathEscape(got.id), "Update"
			identity.target = got.id
		}
		write := &configurationWrite{resource: want.resource(), action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {
			raw, err := e.client.mutate(ctx, method, path, body)
			if err != nil {
				return Task{}, err
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				return Task{}, errInvalidResponse
			}
			return Task{ID: receipt.Response.ID}, nil
		}}
		if err := guardWirelessDefinitionWrite(write, actual, []string{want.name}, e.client.wirelessAPMetadataInventory, func() any { return wirelessAPDispatchIntent(e.desired) }); err != nil {
			return nil, err
		}
		guardWirelessDefinitionPreflight(write, func(ctx context.Context) error {
			// A declared prerequisite may be absent during whole-model planning,
			// but must exist with the exact planned identity at actual dispatch.
			fresh, err := e.dependencySnapshot(ctx, nil, actual)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(dependencies, fresh) {
				return errConfigurationStale
			}
			return nil
		})
		if first == nil {
			first = write
		}
	}
	return first, nil
}
func (e *wirelessAPExecutor) credentialOperation(op *ciskov1.NetworkControllerConfigurationOperation) bool {
	if op == nil {
		return false
	}
	for _, want := range e.desired {
		if op.Resource == want.resource() {
			return len(want.credentials) > 0
		}
	}
	return false
}
func (e *wirelessAPExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	_, ok, err := e.verifyProfile(ctx, op, false)
	return ok, err
}
func (e *wirelessAPExecutor) verifyCredential(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error) {
	return e.verifyProfile(ctx, op, true)
}
func (e *wirelessAPExecutor) verifyProfile(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation, credential bool) (string, bool, error) {
	if op == nil || op.ParentID != "" || op.TargetSerial != "" || op.Action != "Create" && op.Action != "Update" || op.Action == "Create" && op.TargetID != "" || op.Action == "Update" && !validAPIID(op.TargetID) {
		return "", false, errConfigurationTransition
	}
	for _, want := range e.desired {
		if op.Resource != want.resource() {
			continue
		}
		if credential {
			if op.Section != "wireless" || op.ConfigUID != e.owner || len(want.credentials) == 0 || len(op.CredentialVersions) == 0 {
				return "", false, errConfigurationTransition
			}
			_, versions, err := want.payload(e.secrets, e.owner, nil)
			if err != nil || !slices.Equal(versions, op.CredentialVersions) {
				return "", false, errConfigurationSecret
			}
		} else if len(want.credentials) > 0 || len(op.CredentialVersions) > 0 {
			return "", false, errConfigurationSecret
		}
		actual, err := e.client.wirelessAPMetadataInventory(ctx)
		if err != nil {
			return "", false, err
		}
		got, exists := actual[want.name]
		if !exists {
			return "", false, nil
		}
		if op.TargetID != "" && op.TargetID != got.id {
			return "", false, errConfigurationStale
		}
		return got.id, wirelessAPPublicMatches(want.metadata, got.fields), nil
	}
	return "", false, errConfigurationTransition
}
func (e *wirelessAPExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	for _, want := range e.desired {
		if len(want.credentials) > 0 {
			return nil, errConfigurationSecret
		}
	}
	actual, err := e.client.wirelessAPMetadataInventory(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range e.desired {
		got, exists := actual[want.name]
		if exists && wirelessAPPublicMatches(want.metadata, got.fields) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if exists {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "wireless", Resource: want.resource(), Path: "/ap_profiles", Change: change})
	}
	return out, nil
}

// Reject contradictory feature combinations before obtaining a submission permit.
func validateWirelessAPPayload(payload map[string]any) error {
	value := func(path string) any { v, _ := getWirelessAPField(payload, path); return v }
	if value("awipsForensicEnabled") == true && value("awipsEnabled") != true {
		return errWirelessAPModel
	}
	if value("remoteWorkerEnabled") == true && (value("awipsEnabled") == true || value("awipsForensicEnabled") == true || value("rogueDetectionSetting.rogueDetection") == true) {
		return errWirelessAPModel
	}
	nonempty := func(path string) bool { v, ok := value(path).(string); return ok && v != "" }
	auth := value("managementSetting.authType")
	if (auth == "EAP-PEAP" || auth == "EAP-FAST") && (!nonempty("managementSetting.dot1xUsername") || !nonempty("managementSetting.dot1xPassword")) {
		return errConfigurationSecret
	}
	if (value("managementSetting.sshEnabled") == true || value("managementSetting.telnetEnabled") == true) && (!nonempty("managementSetting.managementUserName") || !nonempty("managementSetting.managementPassword")) {
		return errConfigurationSecret
	}
	return nil
}
