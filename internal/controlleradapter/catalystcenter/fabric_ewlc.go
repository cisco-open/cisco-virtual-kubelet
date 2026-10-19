// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
)

type fabricEWLCSettings struct {
	enabled, rolling bool
	percentage       int
}

// Parse only the settings projection. Device identity, fabric role, site and
// managed AP placement must be validated separately by the enclosing compiler.
// Defaults are the pinned NetAsCode 0.5.0 module defaults; explicit null is not
// an instruction to reset or disable a setting.
func parseFabricEWLCSettings(fields map[string]any) (fabricEWLCSettings, error) {
	out := fabricEWLCSettings{enabled: true, percentage: 25}
	for key, value := range fields {
		switch key {
		case "enable_wireless", "enable_rolling_ap_upgrade":
			b, ok := value.(bool)
			if !ok {
				return fabricEWLCSettings{}, errInvalidResponse
			}
			if key == "enable_wireless" {
				out.enabled = b
			} else {
				out.rolling = b
			}
		case "ap_reboot_percentage":
			n, err := plannedAPNumber(value, true)
			if err != nil || (n != 5 && n != 15 && n != 25) {
				return fabricEWLCSettings{}, errInvalidResponse
			}
			out.percentage = int(n)
		default:
			return fabricEWLCSettings{}, errInvalidResponse
		}
	}
	return out, nil
}

type fabricEWLCSetting struct {
	ID      string            `json:"id"`
	Enabled bool              `json:"enableWireless"`
	Rolling fabricEWLCRolling `json:"rollingApUpgrade"`
}

type fabricEWLCRolling struct {
	Enabled    bool `json:"enableRollingApUpgrade"`
	Percentage int  `json:"apRebootPercentage"`
}

func (row *fabricEWLCSetting) UnmarshalJSON(raw []byte) error {
	var wire struct {
		ID      string `json:"id"`
		Enabled *bool  `json:"enableWireless"`
		Rolling *struct {
			Enabled    *bool `json:"enableRollingApUpgrade"`
			Percentage *int  `json:"apRebootPercentage"`
		} `json:"rollingApUpgrade"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&wire) != nil || !validAPIID(wire.ID) || wire.Enabled == nil || wire.Rolling == nil || wire.Rolling.Enabled == nil || wire.Rolling.Percentage == nil {
		return errInvalidResponse
	}
	p := *wire.Rolling.Percentage
	if p != 5 && p != 15 && p != 25 {
		return errInvalidResponse
	}
	*row = fabricEWLCSetting{ID: wire.ID, Enabled: *wire.Enabled, Rolling: fabricEWLCRolling{Enabled: *wire.Rolling.Enabled, Percentage: p}}
	return nil
}

func fabricEWLCPath(fabricID string) (string, error) {
	if !validAPIID(fabricID) {
		return "", errInvalidResponse
	}
	return "/dna/intent/api/v1/sda/fabrics/" + url.PathEscape(fabricID) + "/switchWirelessSetting", nil
}

func fabricEWLCPayload(deviceID string, settings fabricEWLCSettings) (fabricEWLCSetting, error) {
	if !validAPIID(deviceID) || (settings.percentage != 5 && settings.percentage != 15 && settings.percentage != 25) {
		return fabricEWLCSetting{}, errInvalidResponse
	}
	return fabricEWLCSetting{ID: deviceID, Enabled: settings.enabled, Rolling: fabricEWLCRolling{Enabled: settings.rolling, Percentage: settings.percentage}}, nil
}

// The API has no pagination and supports at most two wireless-capable switches
// per fabric site. An absent device is not evidence of completed disablement:
// removal requires a separately verified reload and physical convergence.
func (c *client) fabricEWLCSettings(ctx context.Context, fabricID string) ([]fabricEWLCSetting, error) {
	path, err := fabricEWLCPath(fabricID)
	if err != nil {
		return nil, err
	}
	raw, err := c.get(ctx, path, nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var reply struct {
		Response *[]fabricEWLCSetting `json:"response"`
	}
	if json.Unmarshal(raw, &reply) != nil || reply.Response == nil || len(*reply.Response) > 2 {
		return nil, errInvalidResponse
	}
	rows := *reply.Response
	seen := map[string]bool{}
	for _, row := range rows {
		if seen[row.ID] {
			return nil, errInvalidResponse
		}
		seen[row.ID] = true
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, nil
}
