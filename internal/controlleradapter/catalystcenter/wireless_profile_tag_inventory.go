// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/url"
)

type wirelessProfileTagMetadata struct {
	id, name, profileID string
	fields              map[string]any
	unmapped            bool
}

func wirelessProfileTagPath(profileID string, policy bool) string {
	collection := "siteTags"
	if policy {
		collection = "policyTags"
	}
	return wirelessProfilesPath + "/" + url.PathEscape(profileID) + "/" + collection
}
func (c *client) wirelessProfileTagInventory(ctx context.Context, profileID string, policy bool) (map[string]wirelessProfileTagMetadata, error) {
	if !validAPIID(profileID) {
		return nil, errInvalidResponse
	}
	idKey, nameKey := "siteTagId", "siteTagName"
	schema := map[string]wirelessProfileObservationField{"siteTagId": {kind: "id"}, "siteTagName": {kind: "string"}, "siteIds": {kind: "set"}, "apProfileName": {kind: "string"}, "flexProfileName": {kind: "string"}}
	if policy {
		idKey, nameKey = "policyTagId", "policyTagName"
		schema = map[string]wirelessProfileObservationField{"policyTagId": {kind: "id"}, "policyTagName": {kind: "string"}, "siteIds": {kind: "set"}, "apZones": {kind: "set"}}
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryQueryWithGet(ctx, get, wirelessProfileTagPath(profileID, policy), nil, func(raw json.RawMessage) string {
		var row map[string]any
		if json.Unmarshal(raw, &row) != nil {
			return ""
		}
		id, _ := row[idKey].(string)
		return id
	})
	if err != nil {
		return nil, err
	}
	out := map[string]wirelessProfileTagMetadata{}
	for _, raw := range rows {
		var row map[string]any
		if json.Unmarshal(raw, &row) != nil || row == nil {
			return nil, errInvalidResponse
		}
		// This appliance emits an undocumented null DTO placeholder. It carries
		// no configuration; non-null values remain unknown and block writes.
		if value, present := row["load"]; present && value == nil {
			delete(row, "load")
		}
		fields, unknown, err := projectWirelessProfileObservation(row, schema)
		if err != nil {
			return nil, err
		}
		id, _ := fields[idKey].(string)
		name, _ := fields[nameKey].(string)
		if !validAPIID(id) || !validProfileName(name) {
			return nil, errInvalidResponse
		}
		if _, exists := out[name]; exists {
			return nil, errInvalidResponse
		}
		sites, ok := fields["siteIds"].([]string)
		if !ok {
			return nil, errInvalidResponse
		}
		for _, id := range sites {
			if !validAPIID(id) {
				return nil, errInvalidResponse
			}
		}
		if policy {
			if _, ok := fields["apZones"].([]string); !ok {
				return nil, errInvalidResponse
			}
		}
		delete(fields, idKey)
		out[name] = wirelessProfileTagMetadata{id: id, name: name, profileID: profileID, fields: fields, unmapped: unknown}
	}
	return out, nil
}
