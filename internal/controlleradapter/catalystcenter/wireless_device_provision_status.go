// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// Status is observational evidence, not proof that a particular journaled task
// completed. A future verifier must also correlate task completion and freshness.
// The timestamp's epoch unit is not specified by the API; retain it verbatim.
type wirelessDeviceProvisionStatus struct {
	deviceID, name, siteID, sitePath            string
	networkIntent, modelConfig, lastProvisioned string
	outOfSync                                   bool
}

func validWirelessProvisionStatus(value string) bool {
	switch value {
	case "NOT_STARTED", "SUCCESS", "FAILED", "CONFIGURING", "ROLLBACK_SUCCESS", "ROLLBACK_FAILURE", "ABORTED", "PENDING":
		return true
	}
	return false
}
func (c *client) wirelessDeviceProvisionStatus(ctx context.Context, deviceID string) (wirelessDeviceProvisionStatus, error) {
	var out wirelessDeviceProvisionStatus
	if !validAPIID(deviceID) {
		return out, errInvalidResponse
	}
	raw, err := c.Get(ctx, "/dna/intent/api/v1/wirelessControllers/"+url.PathEscape(deviceID)+"/provisionStatus", nil)
	if err != nil {
		return out, err
	}
	var envelope struct {
		Response *struct {
			DeviceID        string  `json:"networkDeviceId"`
			Name            string  `json:"deviceName"`
			SiteID          string  `json:"siteId"`
			SitePath        string  `json:"siteNameHierarchy"`
			NetworkIntent   string  `json:"networkIntentProvisionStatus"`
			ModelConfig     string  `json:"modelConfigProvisionStatus"`
			LastProvisioned *string `json:"lastProvisionedTimeStamp"`
			OutOfSync       *bool   `json:"outOfSync"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Response == nil {
		return out, errInvalidResponse
	}
	r := envelope.Response
	if r.DeviceID != deviceID || r.Name == "" || len(r.Name) > 256 || strings.TrimSpace(r.Name) != r.Name || strings.ContainsAny(r.Name, "\x00\r\n") || !validWirelessProvisionStatus(r.NetworkIntent) || !validWirelessProvisionStatus(r.ModelConfig) || r.OutOfSync == nil {
		return out, errInvalidResponse
	}
	if r.SiteID != "" && !validAPIID(r.SiteID) || r.SitePath != "" && !validSitePath(r.SitePath) {
		return out, errInvalidResponse
	}
	stamp := ""
	if r.LastProvisioned != nil && *r.LastProvisioned != "null" {
		stamp = *r.LastProvisioned
		n, err := strconv.ParseUint(stamp, 10, 64)
		if err != nil || n == 0 || strconv.FormatUint(n, 10) != stamp {
			return out, errInvalidResponse
		}
	}
	return wirelessDeviceProvisionStatus{deviceID: r.DeviceID, name: r.Name, siteID: r.SiteID, sitePath: r.SitePath, networkIntent: r.NetworkIntent, modelConfig: r.ModelConfig, lastProvisioned: stamp, outOfSync: *r.OutOfSync}, nil
}
