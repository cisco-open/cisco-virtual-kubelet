// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// The pinned NetAsCode provider keys this setting by fabric UUID. It is
// independent of the wired multicast replication-mode endpoint.
func fabricWirelessMulticastPath(fabricID string) (string, error) {
	if !validAPIID(fabricID) {
		return "", errInvalidResponse
	}
	return "/dna/intent/api/v1/sda/fabrics/" + url.PathEscape(fabricID) + "/wirelessMulticast", nil
}

// Missing/unsupported configuration is unknown, never an implicit false. Keep
// this observation separate from mutation admission and ownership decisions.
func (c *client) fabricWirelessMulticast(ctx context.Context, fabricID string) (bool, error) {
	path, err := fabricWirelessMulticastPath(fabricID)
	if err != nil {
		return false, err
	}
	raw, err := c.get(ctx, path, nil, http.StatusOK)
	if err != nil {
		return false, err
	}
	var result struct {
		Response *struct {
			Enabled *bool `json:"multicastEnabled"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Response == nil || result.Response.Enabled == nil {
		return false, errInvalidResponse
	}
	return *result.Response.Enabled, nil
}
