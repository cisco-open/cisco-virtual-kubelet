// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
)

// Empty local settings are distinguishable from populated policy, but APIs do
// not identify whether an empty group came from initial defaults or a reset.
// Retained CVK application receipts remain independent blockers. Credential
// empty objects explicitly disable inheritance and are not treated as absent.
func siteEmptyLocalGroups(response map[string]json.RawMessage, groups []string, emptyAllowed bool) (map[string]string, error) {
	if len(response) != len(groups) {
		return nil, errSiteRetirementReferenced
	}
	out := map[string]string{}
	for _, group := range groups {
		raw, present := response[group]
		if !present {
			return nil, errSiteRetirementReferenced
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			out[group] = "inherited"
			continue
		}
		var fields map[string]json.RawMessage
		if !emptyAllowed || json.Unmarshal(raw, &fields) != nil || fields == nil || len(fields) != 0 {
			return nil, errSiteRetirementReferenced
		}
		out[group] = "empty"
	}
	return out, nil
}

// Reuse the normal local-only API readers. No native values enter this snapshot:
// it contains only fixed group names and inherited/empty shape classifications.
func (c *client) siteEmptyLocalConfiguration(ctx context.Context, id string) (map[string]map[string]string, error) {
	if !canonicalTemplateUUID(id) {
		return nil, errSiteRetirementReferenced
	}
	out := map[string]map[string]string{}
	for _, kind := range []string{"ntp", "dns", "dhcp", "timezone", "banner", "telemetry", "aaa"} {
		response, err := c.networkSettingResponse(ctx, id, kind)
		if err != nil {
			return nil, err
		}
		groups := []string{kind}
		switch kind {
		case "timezone":
			groups = []string{"timeZone"}
		case "telemetry":
			groups = telemetryGroups
		case "aaa":
			groups = nil
			for _, group := range aaaGroups {
				groups = append(groups, group.api)
			}
		}
		shape, err := siteEmptyLocalGroups(response, groups, true)
		if err != nil {
			return nil, err
		}
		out[kind] = shape
	}
	response, err := c.siteCredentialResponse(ctx, id)
	if err != nil {
		return nil, err
	}
	groups := []string{}
	for _, kind := range globalCredentialKinds {
		groups = append(groups, credentialSlot(kind.model))
	}
	shape, err := siteEmptyLocalGroups(response, groups, false)
	if err != nil {
		return nil, err
	}
	out["credentials"] = shape
	return out, nil
}
