// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/url"
)

type wirelessManagedAPLocation struct {
	SiteID   string `json:"siteId"`
	SitePath string `json:"siteNameHierarchy"`
}

func (c *client) wirelessManagedAPLocationCount(ctx context.Context, base, kind string) (int, error) {
	raw, err := c.Get(ctx, base+"/managedApLocations/count", nil)
	if err != nil {
		return 0, err
	}
	var env struct {
		Response map[string]*int `json:"response"`
	}
	if json.Unmarshal(raw, &env) != nil {
		return 0, errInvalidResponse
	}
	for _, k := range []string{"primary", "secondary", "anchor"} {
		n := env.Response[k+"ManagedApLocationsCount"]
		if n == nil || *n < 0 || *n > 50000 {
			return 0, errInvalidResponse
		}
	}
	n := env.Response[kind+"ManagedApLocationsCount"]
	if n == nil {
		return 0, errInvalidResponse
	}
	return *n, nil
}

// The documented response wraps location records in managedApLocations arrays.
// Normalize that envelope before using shared bounded paging and duplicate-ID
// detection. Count checks reject truncated or changing inventories; they do not
// make these separate API reads an atomic snapshot.
func (c *client) wirelessManagedAPLocations(ctx context.Context, deviceID, kind string) (map[string]string, error) {
	if !validAPIID(deviceID) || kind != "primary" && kind != "secondary" && kind != "anchor" {
		return nil, errInvalidResponse
	}
	base := "/dna/intent/api/v1/wirelessControllers/" + url.PathEscape(deviceID)
	expected, err := c.wirelessManagedAPLocationCount(ctx, base, kind)
	if err != nil {
		return nil, err
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		raw, err := c.get(ctx, path, q, 200)
		if err != nil {
			return nil, err
		}
		var env struct {
			Response []struct {
				Locations []wirelessManagedAPLocation `json:"managedApLocations"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &env) != nil || env.Response == nil {
			return nil, errInvalidResponse
		}
		rows := []wirelessManagedAPLocation{}
		for _, group := range env.Response {
			if group.Locations == nil {
				return nil, errInvalidResponse
			}
			rows = append(rows, group.Locations...)
		}
		return json.Marshal(map[string]any{"response": rows})
	}
	rows, err := listInventoryQueryWithGet(ctx, get, base+"/"+kind+"ManagedApLocations", nil, func(row wirelessManagedAPLocation) string { return row.SiteID })
	if err != nil {
		return nil, err
	}
	if len(rows) != expected {
		return nil, errInvalidResponse
	}
	out := map[string]string{}
	for _, row := range rows {
		if !validAPIID(row.SiteID) || !validSitePath(row.SitePath) || row.SitePath == "Global" {
			return nil, errInvalidResponse
		}
		if _, exists := out[row.SitePath]; exists {
			return nil, errInvalidResponse
		}
		out[row.SitePath] = row.SiteID
	}
	final, err := c.wirelessManagedAPLocationCount(ctx, base, kind)
	if err != nil {
		return nil, err
	}
	if final != expected {
		return nil, errConfigurationStale
	}
	return out, nil
}
