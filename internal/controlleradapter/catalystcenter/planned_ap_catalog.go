// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
)

const plannedAPCatalogPath = "/dna/intent/api/v1/maps/supported-access-points"

type plannedAPModel struct {
	Type     string `json:"apType"`
	Patterns []struct {
		Band  string   `json:"band"`
		Names []string `json:"names"`
	} `json:"antennaPatterns"`
}

func plannedAPRetainedModel(want plannedAPIntent, actual plannedAPObservation) plannedAPIntent {
	radios := make([]any, 0, len(actual.Radios))
	for _, radio := range actual.Radios {
		name := ""
		if radio.Antenna != nil {
			name = radio.Antenna.Name
		}
		radios = append(radios, map[string]any{"bands": append([]float64(nil), radio.Bands...), "antenna": map[string]any{"name": name}})
	}
	return plannedAPIntent{floor: want.floor, payload: map[string]any{"type": want.payload["type"], "radios": radios}}
}

// The maps catalog prefixes some antenna labels with a numeric choice marker
// (for example 1_Internal-9130-5GHz). Placement accepts the unprefixed pattern;
// the lab rejects the catalog label itself with NCMP10032. Normalize catalog
// choices for comparison only, never rewrite the manifest or request value.
func plannedAPAntennaPattern(label string) string {
	prefix, name, found := strings.Cut(label, "_")
	if !found || prefix == "" || name == "" {
		return label
	}
	for _, c := range prefix {
		if c < '0' || c > '9' {
			return label
		}
	}
	return name
}

// Map-design model names and antenna patterns are controller catalog values,
// not inventory product IDs. Never translate a product ID by guessing an alias.
func (c *client) validatePlannedAPModels(ctx context.Context, desired []plannedAPIntent) error {
	if len(desired) == 0 {
		return nil
	}
	raw, err := c.get(ctx, plannedAPCatalogPath, nil, http.StatusOK)
	if err != nil {
		return err
	}
	var rows []plannedAPModel
	if json.Unmarshal(raw, &rows) != nil || rows == nil || len(rows) > 10000 {
		return errInvalidResponse
	}
	models := map[string]plannedAPModel{}
	for _, row := range rows {
		if _, err := plannedAPText(row.Type); err != nil {
			return errInvalidResponse
		}
		if _, duplicate := models[row.Type]; duplicate {
			return errInvalidResponse
		}
		models[row.Type] = row
	}
	for _, want := range desired {
		kind, ok := want.payload["type"].(string)
		model, exists := models[kind]
		if !ok || !exists {
			return errPlannedAPModel
		}
		available := map[string]map[string]bool{}
		for _, pattern := range model.Patterns {
			if pattern.Band != "2.4" && pattern.Band != "5" && pattern.Band != "6" {
				return errInvalidResponse
			}
			if _, duplicate := available[pattern.Band]; duplicate || len(pattern.Names) == 0 {
				return errInvalidResponse
			}
			names := map[string]bool{}
			for _, name := range pattern.Names {
				name = plannedAPAntennaPattern(name)
				if _, err := plannedAPText(name); err != nil || names[name] {
					return errInvalidResponse
				}
				names[name] = true
			}
			available[pattern.Band] = names
		}
		if len(available) == 0 {
			return errInvalidResponse
		}
		radios, _ := want.payload["radios"].([]any)
		for _, value := range radios {
			radio, ok := value.(map[string]any)
			if !ok {
				return errPlannedAPModel
			}
			antenna, _ := radio["antenna"].(map[string]any)
			name, selected := antenna["name"].(string)
			bands, _ := radio["bands"].([]float64)
			if len(bands) == 0 && selected {
				found := false
				for _, names := range available {
					found = found || names[name]
				}
				if !found {
					return errPlannedAPModel
				}
			}
			for _, band := range bands {
				names := available[strconv.FormatFloat(band, 'f', -1, 64)]
				if len(names) == 0 || selected && !names[name] {
					return errPlannedAPModel
				}
			}
		}
	}
	return nil
}
