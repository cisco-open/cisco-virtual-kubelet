// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

type fabricVLANSSID struct {
	VLAN  string              `json:"vlanName"`
	SSIDs []fabricSSIDBinding `json:"ssidDetails"`
}
type fabricSSIDBinding struct {
	Name          string  `json:"name"`
	SecurityGroup *string `json:"securityGroupTag,omitempty"`
}

// Replacement writes cannot reconstruct policy that their native projection
// does not understand. Reject unknown fields (including nested SSID fields)
// during observation, rather than returning apparently complete inventory.
func (row *fabricVLANSSID) UnmarshalJSON(raw []byte) error {
	type wireMapping fabricVLANSSID
	var parsed wireMapping
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&parsed); err != nil {
		return errInvalidResponse
	}
	*row = fabricVLANSSID(parsed)
	return nil
}

func fabricVLANSSIDPath(fabricID string) (string, error) {
	if !validAPIID(fabricID) {
		return "", errInvalidResponse
	}
	return "/dna/intent/api/v1/sda/fabrics/" + url.PathEscape(fabricID) + "/vlanToSsids", nil
}

// Read the complete fabric-scoped mapping. SSID names are case sensitive; an
// SSID may not resolve to two different VLANs in the same fabric. Preserve the
// optional group tag so future writes cannot discard unrelated policy.
func (c *client) fabricVLANSSIDs(ctx context.Context, fabricID string) ([]fabricVLANSSID, error) {
	path, err := fabricVLANSSIDPath(fabricID)
	if err != nil {
		return nil, err
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	rows, err := listInventoryPagesWithGet(ctx, get, path, nil, func(row fabricVLANSSID) string { return row.VLAN }, 50)
	if err != nil {
		return nil, err
	}
	return canonicalFabricVLANSSIDs(rows)
}
