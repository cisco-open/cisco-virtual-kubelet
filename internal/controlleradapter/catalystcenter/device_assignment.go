// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"net/url"
)

const assignedDevicesPath = "/dna/intent/api/v1/networkDevices/assignedToSite"

// Assignment evidence is separate from inventory discovery: existence in the
// inventory is not proof that a requested ASSIGN state has been reached.
func (c *client) assignedDevices(ctx context.Context, siteID string) (map[string]bool, error) {
	if !validAPIID(siteID) {
		return nil, errInvalidResponse
	}
	type assignment struct {
		DeviceID string `json:"deviceId"`
		SiteID   string `json:"siteId"`
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	items, err := listInventoryQueryWithGet(ctx, get, assignedDevicesPath, url.Values{"siteId": {siteID}}, func(a assignment) string { return a.DeviceID })
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	for _, a := range items {
		if a.SiteID != siteID {
			return nil, errInvalidResponse
		}
		result[a.DeviceID] = true
	}
	return result, nil
}
