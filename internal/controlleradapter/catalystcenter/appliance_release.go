// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"strings"
)

const applianceReleasePath = "/dna/intent/api/v1/dnac-release"

// Release observation is shared, but each feature retains its own compatibility
// policy. In particular, SWIM's exact qualified-build fence must not broaden.
func (c *client) installedRelease(ctx context.Context) (string, error) {
	raw, err := c.get(ctx, applianceReleasePath, nil, 200)
	if err != nil {
		return "", err
	}
	var envelope struct {
		Response struct {
			InstalledVersion string `json:"installedVersion"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return "", errInvalidResponse
	}
	version := envelope.Response.InstalledVersion
	if version == "" || len(version) > 128 || strings.TrimSpace(version) != version || strings.ContainsAny(version, "\x00\r\n") {
		return "", errInvalidResponse
	}
	return version, nil
}
