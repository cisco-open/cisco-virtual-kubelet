// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const compliancePath = "/dna/intent/api/v1/compliance/"

type complianceEvidence struct {
	deviceID, kind, status, state string
	checkedAt                     time.Time
}

// Read selected typed intent evidence without retaining violation descriptions,
// intended/configured values, business keys or free-form text. No mutation,
// compliance refresh, remediation or journal transition is performed here.
func (c *client) readIntentCompliance(ctx context.Context, deviceID string) (map[string]complianceEvidence, error) {
	if !validAPIID(deviceID) {
		return nil, errInvalidResponse
	}
	// The lab returns empty data for combined category/type filters despite
	// having reports. Read once and project locally; do not retry/fallback or
	// request diff values, remediation, or a refresh.
	raw, err := c.get(ctx, compliancePath+deviceID+"/detail", url.Values{"diffList": {"false"}}, http.StatusOK)
	if err != nil {
		return nil, err
	}
	return parseIntentCompliance(raw, deviceID)
}

func parseIntentCompliance(raw []byte, deviceID string) (map[string]complianceEvidence, error) {
	var envelope struct {
		DeviceID string `json:"deviceUuid"`
		Response []struct {
			DeviceID string          `json:"deviceUuid"`
			Kind     string          `json:"complianceType"`
			Status   string          `json:"status"`
			State    string          `json:"state"`
			Updated  json.RawMessage `json:"lastUpdateTime"`
		} `json:"response"`
	}
	if !validAPIID(deviceID) || json.Unmarshal(raw, &envelope) != nil || envelope.Response == nil || len(envelope.Response) > 1000 {
		return nil, errInvalidResponse
	}
	// The lab appliance returns a null UUID and an empty list when no report
	// exists. Preserve that absence, never convert it into positive evidence.
	if envelope.DeviceID == "" && len(envelope.Response) == 0 {
		return map[string]complianceEvidence{}, nil
	}
	if envelope.DeviceID != deviceID {
		return nil, errInvalidResponse
	}
	evidence := map[string]complianceEvidence{}
	for _, row := range envelope.Response {
		if row.DeviceID != deviceID || row.Kind == "" {
			return nil, errInvalidResponse
		}
		if row.Kind != "NETWORK_SETTINGS" && row.Kind != "NETWORK_PROFILE" {
			continue // unrelated domains cannot supply selected intent evidence
		}
		if _, exists := evidence[row.Kind]; exists {
			return nil, errInvalidResponse
		}
		switch row.Status {
		case "COMPLIANT", "NON_COMPLIANT", "ERROR", "IN_PROGRESS", "NOT_APPLICABLE", "NOT_AVAILABLE", "COMPLIANT_WARNING", "REMEDIATION_IN_PROGRESS", "ABORTED":
		default:
			return nil, errInvalidResponse
		}
		switch row.State {
		case "SUCCESS", "FAILED", "IN_PROGRESS":
		default:
			return nil, errInvalidResponse
		}
		var checked time.Time
		if len(row.Updated) != 0 && string(row.Updated) != "null" {
			millis, err := strconv.ParseInt(string(row.Updated), 10, 64)
			if err != nil || millis <= 0 {
				return nil, errInvalidResponse
			}
			// Only millisecond epoch evidence is accepted. Reject second timestamps,
			// overflow and unrelated date ranges rather than guessing units.
			checked = time.UnixMilli(millis).UTC()
			if checked.Year() < 2000 || checked.Year() > 9999 {
				return nil, errInvalidResponse
			}
		}
		evidence[row.Kind] = complianceEvidence{deviceID, row.Kind, row.Status, row.State, checked}
	}
	return evidence, nil
}

// A strict supplemental predicate. The floor must come from independently
// established task completion, not process uptime or the task submission time.
// This still cannot attest source/site revision or replace actual device proof.
func freshIntentCompliance(evidence map[string]complianceEvidence, deviceID string, kinds []string, completed, now time.Time) error {
	if !validAPIID(deviceID) || len(kinds) == 0 || completed.IsZero() || now.IsZero() || completed.After(now) {
		return errConfigurationTransition
	}
	seen := map[string]bool{}
	for _, kind := range kinds {
		if kind != "NETWORK_SETTINGS" && kind != "NETWORK_PROFILE" || seen[kind] {
			return errConfigurationTransition
		}
		seen[kind] = true
		row, exists := evidence[kind]
		if !exists {
			return errConfigurationBusy
		}
		if row.deviceID != deviceID || row.kind != kind {
			return errConfigurationStale
		}
		if row.checkedAt.IsZero() || row.checkedAt.Before(completed) {
			return errConfigurationBusy
		}
		if row.checkedAt.After(now) {
			return errConfigurationStale
		}
		if row.status != "COMPLIANT" || row.state != "SUCCESS" {
			return errConfigurationBusy
		}
	}
	return nil
}
