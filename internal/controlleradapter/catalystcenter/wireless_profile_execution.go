// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type wirelessProfileExecutor struct {
	client  *client
	desired []wirelessProfileIntent
}

func (e *wirelessProfileExecutor) count() int { return len(e.desired) }

// Bind only dependencies used by this payload. Unrelated inventory changes
// must not invalidate an operation. Resource carries the non-secret digest;
// ParentID and TargetID retain their existing Global/profile identity meaning.
func wirelessProfileBinding(payload map[string]any, refs wirelessProfileReferences) (string, error) {
	bindings := map[string]string{"global": refs.global}
	if !validAPIID(refs.global) {
		return "", errInvalidResponse
	}
	add := func(kind, name, id string) error {
		if name == "" || !validAPIID(id) {
			return errInvalidResponse
		}
		bindings[kind+":"+name] = id
		return nil
	}
	entries, _ := payload["ssidDetails"].([]map[string]any)
	for _, entry := range entries {
		name, _ := entry["ssidName"].(string)
		if err := add("ssid", name, refs.ssids[name].id); err != nil {
			return "", err
		}
		for field, index := range map[string]map[string]string{"interfaceName": refs.interfaces, "anchorGroupName": refs.anchors} {
			if name, _ := entry[field].(string); name != "" {
				if err := add(field, name, index[name]); err != nil {
					return "", err
				}
			}
		}
		if selector, exists := entry["vlanGroupName"]; exists {
			value, ok := selector.(string)
			if !ok || value != "" && !validProfileName(value) {
				return "", errInvalidResponse
			}
			if value != "" {
				bindings["vlan-group-selector:"+name] = value
			}
		}
		if id, _ := entry["dot11beProfileId"].(string); id != "" {
			if err := add("dot11be", id, id); err != nil {
				return "", err
			}
		}
	}
	interfaces, _ := payload["additionalInterfaces"].([]string)
	for _, name := range interfaces {
		if err := add("interfaceName", name, refs.interfaces[name]); err != nil {
			return "", err
		}
	}
	zones, _ := payload["apZones"].([]map[string]any)
	for _, zone := range zones {
		if name, _ := zone["rfProfileName"].(string); name != "" {
			if err := add("rf", name, refs.rf[name]); err != nil {
				return "", err
			}
		}
	}
	features, _ := payload["featureTemplates"].([]map[string]any)
	for _, feature := range features {
		id, _ := feature["id"].(string)
		if err := add("feature", id, id); err != nil {
			return "", err
		}
	}
	raw, err := json.Marshal(bindings)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func wirelessProfileRetainsMembers(old, next map[string]any) bool {
	for collection, key := range map[string]string{"ssidDetails": "ssidName", "apZones": "apZoneName", "featureTemplates": "id"} {
		prior, _ := old[collection].([]map[string]any)
		desired, _ := next[collection].([]map[string]any)
		names := map[string]bool{}
		for _, entry := range desired {
			name, _ := entry[key].(string)
			names[name] = true
		}
		for _, entry := range prior {
			name, _ := entry[key].(string)
			if !names[name] {
				return false
			}
		}
	}
	prior, _ := old["additionalInterfaces"].([]string)
	desired, _ := next["additionalInterfaces"].([]string)
	names := map[string]bool{}
	for _, name := range desired {
		names[name] = true
	}
	for _, name := range prior {
		if !names[name] {
			return false
		}
	}
	return true
}
func (e *wirelessProfileExecutor) next(ctx context.Context) (*configurationWrite, error) {
	actual, err := e.client.wirelessProfileInventory(ctx)
	if err != nil {
		return nil, err
	}
	var first *configurationWrite
	for _, want := range e.desired {
		got, exists := actual[want.name]
		var observed *wirelessProfileMetadata
		if exists {
			observed = &got
		}
		refs, err := e.client.wirelessProfileReferences(ctx, want, observed)
		if err != nil {
			return nil, err
		}
		payload, err := want.payload(refs, observed)
		if err != nil {
			return nil, err
		}
		if exists && reflect.DeepEqual(payload, got.fields) {
			continue
		}
		if exists && !wirelessProfileRetainsMembers(got.fields, payload) {
			return nil, errConfigurationOwned
		}
		binding, err := wirelessProfileBinding(payload, refs)
		if err != nil {
			return nil, err
		}
		resource := "wireless-profile:" + want.name + ":" + binding
		if len(resource) > 512 {
			return nil, errConfigurationTransition
		}
		method, path, action := http.MethodPost, wirelessProfilesPath, "Create"
		identity := configurationResourceIdentity{parent: refs.global}
		if exists {
			method, path, action = http.MethodPut, path+"/"+url.PathEscape(got.id), "Update"
			identity.target = got.id
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		write := &configurationWrite{resource: resource, action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {
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
			return receipt.Response, nil
		}}
		intent, err := json.Marshal(wirelessProfileDispatchIntent(e.desired))
		if err != nil {
			return nil, err
		}
		guardConfigurationPreflight(write, func(ctx context.Context) error {
			freshIntent, err := json.Marshal(wirelessProfileDispatchIntent(e.desired))
			if err != nil || !bytes.Equal(intent, freshIntent) {
				return errConfigurationStale
			}
			// Re-read identity and dependencies immediately before submission as well
			// as at journal admission; never replay if this check fails.
			current, err := e.client.wirelessProfileInventory(ctx)
			if err != nil {
				return err
			}
			currentProfile, present := current[want.name]
			if present != exists || present && !reflect.DeepEqual(currentProfile, got) {
				return errConfigurationStale
			}
			var observed *wirelessProfileMetadata
			if present {
				observed = &currentProfile
			}
			fresh, err := e.client.wirelessProfileReferences(ctx, want, observed)
			if err != nil {
				return err
			}
			next, err := want.payload(fresh, observed)
			if err != nil {
				return err
			}
			fingerprint, err := wirelessProfileBinding(next, fresh)
			encoded, encodeErr := json.Marshal(next)
			if err != nil || encodeErr != nil || fingerprint != binding || !bytes.Equal(encoded, body) {
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
func (e *wirelessProfileExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.TargetSerial != "" || len(op.CredentialVersions) > 0 || !validAPIID(op.ParentID) || op.Action != "Create" && op.Action != "Update" || op.Action == "Create" && op.TargetID != "" || op.Action == "Update" && !validAPIID(op.TargetID) {
		return false, errConfigurationTransition
	}
	actual, err := e.client.wirelessProfileInventory(ctx)
	if err != nil {
		return false, err
	}
	for _, want := range e.desired {
		prefix := "wireless-profile:" + want.name + ":"
		if !strings.HasPrefix(op.Resource, prefix) || len(op.Resource) != len(prefix)+64 {
			continue
		}
		got, exists := actual[want.name]
		if !exists {
			return false, nil
		}
		if op.TargetID != "" && op.TargetID != got.id {
			return false, errConfigurationStale
		}
		refs, err := e.client.wirelessProfileReferences(ctx, want, &got)
		if err != nil {
			return false, err
		}
		if refs.global != op.ParentID {
			return false, errConfigurationStale
		}
		payload, err := want.payload(refs, &got)
		if err != nil {
			return false, err
		}
		binding, err := wirelessProfileBinding(payload, refs)
		if err != nil {
			return false, err
		}
		if op.Resource != prefix+binding {
			return false, errConfigurationStale
		}
		return reflect.DeepEqual(payload, got.fields), nil
	}
	return false, errConfigurationTransition
}

func (e *wirelessProfileExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	actual, err := e.client.wirelessProfileInventory(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range e.desired {
		got, exists := actual[want.name]
		var observed *wirelessProfileMetadata
		if exists {
			observed = &got
		}
		refs, err := e.client.wirelessProfileReferences(ctx, want, observed)
		if err != nil {
			return nil, err
		}
		payload, err := want.payload(refs, observed)
		if err != nil {
			return nil, err
		}
		if exists && reflect.DeepEqual(payload, got.fields) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if exists {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "network_profiles", Resource: "wireless-profile:" + want.name, Path: "/wireless", Change: change})
	}
	return out, nil
}
