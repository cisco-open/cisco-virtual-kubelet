// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

// A received task ID is exact, but controller and worker clocks can differ.
// Keep freshness bounded; larger drift requires fixing time synchronization.
const configurationTaskClockSkew = 2 * time.Minute

func validateSiteExecution(desired []desiredSite) error {
	for _, d := range desired {
		if utf8.RuneCountInString(lastSiteName(d.path)) > 40 {
			return errors.New("site execution names must not exceed 40 characters")
		}
		if d.kind == "floor" && d.fields["floor_number"] == nil {
			return errors.New("floor execution requires explicit floor_number")
		}
		if d.kind == "floor" {
			for _, key := range []string{"width", "length", "height"} {
				value, ok := d.fields[key].(float64)
				if !ok || normalizedSiteNumber(key, value) <= 0 {
					return errors.New("floor geometry must remain positive at supported precision")
				}
			}
		}
	}
	return nil
}

// siteMutation is an in-memory, single-resource execution plan. Payloads never
// enter Kubernetes status. Every step is replanned from a complete site read;
// children wait until their parent's actual controller ID is available.
type siteMutation struct {
	desired                                desiredSite
	id, parentID, action, method, endpoint string
	body                                   []byte
}

func nextSiteMutation(desired []desiredSite, observed []Site) (*siteMutation, error) {
	drift, err := planSites(desired, observed)
	if err != nil {
		return nil, err
	}
	// Resolve all locally checkable required attributes before any parent is
	// created. Reporting can intentionally select fewer fields than applying.
	if err := validateSiteExecution(desired); err != nil {
		return nil, err
	}
	if len(drift) == 0 {
		return nil, nil
	}
	actual := map[string]Site{}
	for _, s := range observed {
		actual[s.NameHierarchy] = s
	}
	changed := map[string]bool{}
	for _, d := range drift {
		changed[d.Resource] = true
	}
	ordered := append([]desiredSite(nil), desired...)
	sort.Slice(ordered, func(i, j int) bool {
		left, right := strings.Count(ordered[i].path, "/"), strings.Count(ordered[j].path, "/")
		if left != right {
			return left < right
		}
		return ordered[i].path < ordered[j].path
	})
	for _, d := range ordered {
		if !changed[d.path] {
			continue
		}
		parent, exists := actual[parentSitePath(d.path)]
		if !exists {
			continue
		}
		if !validAPIID(parent.ID) {
			return nil, errInvalidResponse
		}
		m := &siteMutation{desired: d, parentID: parent.ID, action: "Create", method: http.MethodPost}
		m.endpoint = siteDesignMutationEndpoint(d.kind)
		if m.endpoint == "" {
			return nil, errInvalidResponse
		}
		payload := map[string]any{"name": lastSiteName(d.path), "parentId": parent.ID}
		if existing, ok := actual[d.path]; ok {
			if !validAPIID(existing.ID) {
				return nil, errInvalidResponse
			}
			m.id, m.action, m.method = existing.ID, "Update", http.MethodPut
			m.endpoint += "/" + existing.ID
			// Preserve unselected supported building fields during PUT. Floors
			// have a complete resolved geometry requirement in parseSites.
			if d.kind == "building" {
				if existing.Country != nil {
					payload["country"] = *existing.Country
				}
				if existing.Address != nil {
					payload["address"] = *existing.Address
				}
				if existing.Latitude != nil {
					payload["latitude"] = *existing.Latitude
				}
				if existing.Longitude != nil {
					payload["longitude"] = *existing.Longitude
				}
			}
		}
		keys := map[string]string{"address": "address", "country": "country", "latitude": "latitude", "longitude": "longitude", "floor_number": "floorNumber", "rf_model": "rfModel", "width": "width", "length": "length", "height": "height", "units_of_measure": "unitsOfMeasure"}
		for key, value := range d.fields {
			apiKey, ok := keys[key]
			if !ok {
				return nil, errInvalidResponse
			}
			if n, ok := value.(float64); ok {
				payload[apiKey] = normalizedSiteNumber(key, n)
			} else {
				payload[apiKey] = value
			}
		}
		m.body, err = json.Marshal(payload)
		if err != nil {
			return nil, errors.New("cannot encode site operation")
		}
		return m, nil
	}
	return nil, errors.New("site dependencies cannot be resolved from complete inventory")
}

func siteDesignMutationEndpoint(kind string) string {
	switch kind {
	case "area":
		return "/dna/intent/api/v1/areas"
	case "building":
		return "/dna/intent/api/v2/buildings"
	case "floor":
		return "/dna/intent/api/v2/floors"
	default:
		return ""
	}
}

func (c *client) submitSiteMutation(ctx context.Context, m *siteMutation) (Task, error) {
	if m == nil {
		return Task{}, errInvalidResponse
	}
	deleteEndpoint := siteDesignMutationEndpoint(m.desired.kind)
	deleting := m.method == http.MethodDelete && m.action == "Delete" && len(m.body) == 0 && deleteEndpoint != "" && canonicalTemplateUUID(m.id) && validAPIID(m.parentID) && m.id != m.parentID && m.endpoint == deleteEndpoint+"/"+m.id
	writing := len(m.body) > 0 && (m.action == "Create" && m.method == http.MethodPost || m.action == "Update" && m.method == http.MethodPut)
	if !deleting && !writing {
		return Task{}, errInvalidResponse
	}
	raw, err := c.mutate(ctx, m.method, m.endpoint, m.body)
	if err != nil {
		return Task{}, err
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		return Task{}, errInvalidResponse
	}
	// Ignore the response URL. Task reads always use our configured origin and
	// validated ID, never an appliance-supplied URL.
	return Task{ID: receipt.Response.ID}, nil
}

// configurationTaskComplete correlates the exact receipt and submission time.
// Task failure is an error, not proof that no configuration was changed.
// A successful terminal task still requires resource-specific read-back.
func (c *client) configurationTaskComplete(ctx context.Context, id string, submitted, now time.Time) (bool, error) {
	if !validAPIID(id) || submitted.IsZero() || now.Before(submitted) {
		return false, errInvalidResponse
	}
	raw, err := c.get(ctx, taskPath+id, nil, http.StatusOK)
	if err != nil {
		return false, err
	}
	return decodeConfigurationTaskComplete(raw, id, submitted, now)
}

// Validate one task observation without interpreting product-specific progress.
// Result adapters can bind a native UUID from these same validated bytes.
func decodeConfigurationTaskComplete(raw []byte, id string, submitted, now time.Time) (bool, error) {
	if !validAPIID(id) || submitted.IsZero() || now.Before(submitted) {
		return false, errInvalidResponse
	}
	var envelope struct {
		Response *struct {
			ID    string `json:"id"`
			Code  string `json:"errorCode"`
			Start *int64 `json:"startTime"`
			End   *int64 `json:"endTime"`
			Error *bool  `json:"isError"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &envelope) != nil || envelope.Response == nil {
		return false, errInvalidResponse
	}
	task := envelope.Response
	if task.ID != id || task.Start == nil || task.Error == nil || *task.Start < submitted.Add(-configurationTaskClockSkew).UnixMilli() || *task.Start > now.Add(configurationTaskClockSkew).UnixMilli() {
		return false, errInvalidResponse
	}
	if *task.Error {
		return false, newConfigurationTaskFailure(task.Code)
	}
	if task.End == nil || *task.End == 0 {
		return false, nil
	}
	if *task.End < *task.Start || *task.End > now.Add(configurationTaskClockSkew).UnixMilli() {
		return false, errInvalidResponse
	}
	return true, nil
}

func (c *client) verifySiteMutation(ctx context.Context, m *siteMutation) (bool, error) {
	if m == nil {
		return false, errInvalidResponse
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		return false, err
	}
	for _, site := range sites {
		if site.NameHierarchy != m.desired.path {
			continue
		}
		if site.ParentID != m.parentID || (m.id != "" && site.ID != m.id) {
			return false, errors.New("site identity changed during operation")
		}
		drift, err := planSites([]desiredSite{m.desired}, sites)
		return err == nil && len(drift) == 0, err
	}
	return false, nil
}
