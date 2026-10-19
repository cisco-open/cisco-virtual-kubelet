// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strings"
)

const wirelessProfilesPath = "/dna/intent/api/v1/wirelessProfiles"

type wirelessProfileMetadata struct {
	id, name string
	fields   map[string]any
	unmapped bool
}

// This schema projects public profile data only. Unknown fields are never
// retained and mark the object unsafe for a full-object update.
type wirelessProfileObservationField struct {
	kind     string
	fields   map[string]wirelessProfileObservationField
	identity string
}

var wirelessProfileObservation = map[string]wirelessProfileObservationField{
	"id": {kind: "id"}, "wirelessProfileName": {kind: "string"},
	"additionalInterfaces": {kind: "set"},
	"ssidDetails": {kind: "list", identity: "ssidName", fields: map[string]wirelessProfileObservationField{
		// Returned by the appliance for the referenced SSID. The pinned NaC
		// profile model does not own this field; preserve it on full updates.
		"policyProfileName": {kind: "string"},
		"ssidName":          {kind: "string"}, "enableFabric": {kind: "bool"}, "interfaceName": {kind: "string"}, "vlanGroupName": {kind: "string"}, "wlanProfileName": {kind: "string"}, "dot11beProfileId": {kind: "optional-id"}, "anchorGroupName": {kind: "string"},
		"flexConnect": {kind: "object", fields: map[string]wirelessProfileObservationField{"enableFlexConnect": {kind: "bool"}, "localToVlan": {kind: "int"}}},
	}},
	"apZones":          {kind: "list", identity: "apZoneName", fields: map[string]wirelessProfileObservationField{"apZoneName": {kind: "string"}, "rfProfileName": {kind: "string"}, "ssids": {kind: "set"}}},
	"featureTemplates": {kind: "list", identity: "id", fields: map[string]wirelessProfileObservationField{"id": {kind: "id"}, "ssids": {kind: "set"}}},
}

func projectWirelessProfileObservation(input map[string]any, schema map[string]wirelessProfileObservationField) (map[string]any, bool, error) {
	out := map[string]any{}
	unmapped := false
	for key, value := range input {
		field, known := schema[key]
		if !known {
			unmapped = true
			continue
		}
		if value == nil {
			continue
		}
		switch field.kind {
		case "string", "id", "optional-id":
			s, ok := value.(string)
			if !ok || strings.ContainsAny(s, "\x00\r\n") || field.kind == "id" && !validAPIID(s) || field.kind == "optional-id" && s != "" && !validAPIID(s) {
				return nil, false, errInvalidResponse
			}
			out[key] = s
		case "bool":
			flag, ok := value.(bool)
			if !ok {
				return nil, false, errInvalidResponse
			}
			out[key] = flag
		case "int":
			n, err := wirelessAPInteger(value)
			if err != nil {
				return nil, false, errInvalidResponse
			}
			out[key] = n
		case "set":
			rows, ok := value.([]any)
			if !ok {
				return nil, false, errInvalidResponse
			}
			values := []string{}
			seen := map[string]bool{}
			for _, row := range rows {
				s, ok := row.(string)
				if !ok || s == "" || strings.ContainsAny(s, "\x00\r\n") || seen[s] {
					return nil, false, errInvalidResponse
				}
				seen[s] = true
				values = append(values, s)
			}
			sort.Strings(values)
			out[key] = values
		case "object":
			object, ok := value.(map[string]any)
			if !ok {
				return nil, false, errInvalidResponse
			}
			projected, unknown, err := projectWirelessProfileObservation(object, field.fields)
			if err != nil {
				return nil, false, err
			}
			unmapped = unmapped || unknown
			out[key] = projected
		case "list":
			rows, ok := value.([]any)
			if !ok {
				return nil, false, errInvalidResponse
			}
			values := []map[string]any{}
			seen := map[string]bool{}
			for _, row := range rows {
				object, ok := row.(map[string]any)
				if !ok {
					return nil, false, errInvalidResponse
				}
				projected, unknown, err := projectWirelessProfileObservation(object, field.fields)
				if err != nil {
					return nil, false, err
				}
				id, ok := projected[field.identity].(string)
				if !ok || id == "" || seen[id] {
					return nil, false, errInvalidResponse
				}
				seen[id] = true
				unmapped = unmapped || unknown
				values = append(values, projected)
			}
			sort.Slice(values, func(i, j int) bool { return values[i][field.identity].(string) < values[j][field.identity].(string) })
			out[key] = values
		default:
			return nil, false, errInvalidResponse
		}
	}
	return out, unmapped, nil
}
func (c *client) wirelessProfileInventory(ctx context.Context) (map[string]wirelessProfileMetadata, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryQueryWithGet(ctx, get, wirelessProfilesPath, nil, func(raw json.RawMessage) string {
		var row struct {
			ID string `json:"id"`
		}
		_ = json.Unmarshal(raw, &row)
		return row.ID
	})
	if err != nil {
		return nil, err
	}
	out := map[string]wirelessProfileMetadata{}
	for _, raw := range rows {
		var row map[string]any
		if json.Unmarshal(raw, &row) != nil || row == nil {
			return nil, errInvalidResponse
		}
		fields, unmapped, err := projectWirelessProfileObservation(row, wirelessProfileObservation)
		if err != nil {
			return nil, err
		}
		id, _ := fields["id"].(string)
		name, _ := fields["wirelessProfileName"].(string)
		if !validAPIID(id) || !validProfileName(name) {
			return nil, errInvalidResponse
		}
		if _, exists := out[name]; exists {
			return nil, errInvalidResponse
		}
		delete(fields, "id")
		out[name] = wirelessProfileMetadata{id: id, name: name, fields: fields, unmapped: unmapped}
	}
	return out, nil
}
