// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	yamlv3 "go.yaml.in/yaml/v3"
)

const sitesPath = "/dna/intent/api/v1/sites"

// Site is the modern Site Design API projection, not the legacy /site shape.
// Pointer fields distinguish a missing observation from a legitimate zero.
type Site struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	NameHierarchy  string   `json:"nameHierarchy"`
	ParentID       string   `json:"parentId"`
	Type           string   `json:"type"`
	Address        *string  `json:"address"`
	Country        *string  `json:"country"`
	Latitude       *float64 `json:"latitude"`
	Longitude      *float64 `json:"longitude"`
	FloorNumber    *float64 `json:"floorNumber"`
	RFModel        *string  `json:"rfModel"`
	Width          *float64 `json:"width"`
	Length         *float64 `json:"length"`
	Height         *float64 `json:"height"`
	UnitsOfMeasure *string  `json:"unitsOfMeasure"`
}

func (c *client) ListSites(ctx context.Context) ([]Site, error) {
	get := func(ctx context.Context, path string, query url.Values) ([]byte, error) {
		return c.get(ctx, path, query, 200)
	}
	sites, err := listInventoryQueryWithGet(ctx, get, sitesPath, url.Values{}, func(s Site) string { return s.ID })
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, s := range sites {
		if !validSitePath(s.NameHierarchy) || s.Name != lastSiteName(s.NameHierarchy) {
			return nil, errInvalidResponse
		}
		if _, ok := byPath[s.NameHierarchy]; ok {
			return nil, errInvalidResponse
		}
		switch s.Type {
		case "global":
			if s.NameHierarchy != "Global" {
				return nil, errInvalidResponse
			}
		case "area", "building", "floor":
			if s.NameHierarchy == "Global" {
				return nil, errInvalidResponse
			}
		default:
			return nil, errInvalidResponse
		}
		byPath[s.NameHierarchy] = s
	}
	// A missing root/parent is an incomplete observation, not absence evidence.
	if root, ok := byPath["Global"]; !ok || root.Type != "global" {
		return nil, errInvalidResponse
	}
	for _, s := range sites {
		if s.NameHierarchy == "Global" {
			continue
		}
		parent, ok := byPath[parentSitePath(s.NameHierarchy)]
		if !ok || parent.ID != s.ParentID || !validSiteParent(s.Type, parent.Type) {
			return nil, errInvalidResponse
		}
	}
	return sites, nil
}
func validSitePath(s string) bool {
	if len(s) > 512 || (s != "Global" && !strings.HasPrefix(s, "Global/")) {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimSpace(part) != part || strings.ContainsAny(part, "\r\n\t") {
			return false
		}
	}
	return true
}
func lastSiteName(s string) string { p := strings.Split(s, "/"); return p[len(p)-1] }
func parentSitePath(s string) string {
	i := strings.LastIndex(s, "/")
	if i < 0 {
		return ""
	}
	return s[:i]
}
func validSiteParent(kind, parent string) bool {
	switch kind {
	case "area", "building":
		return parent == "area" || parent == "global"
	case "floor":
		return parent == "building"
	}
	return false
}

type desiredSite struct {
	kind, path string
	fields     map[string]any
}

// parseSites accepts resolved site attributes only. Floor maps and AP placement
// are rejected explicitly until their independent contracts are implemented.
func parseSites(raw []byte) ([]desiredSite, error) {
	stripe, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	if len(stripe.Content) != 2 || stripe.Content[0].Value != "sites" {
		return nil, errors.New("only the sites section can be reported")
	}
	node := stripe.Content[1]
	if node.Kind != yamlv3.MappingNode {
		return nil, errors.New("sites must be an object")
	}
	for i := 0; i < len(node.Content); i += 2 {
		if node.Content[i+1].Kind != yamlv3.SequenceNode {
			return nil, errors.New("site collections must be non-null lists")
		}
	}
	var groups map[string][]map[string]any
	if node.Decode(&groups) != nil {
		return nil, errors.New("sites must contain lists of site objects")
	}
	out := []desiredSite{}
	seen := map[string]bool{}
	for group, entries := range groups {
		kind := ""
		switch group {
		case "areas":
			kind = "area"
		case "buildings":
			kind = "building"
		case "floors":
			kind = "floor"
		default:
			return nil, errors.New("unsupported sites collection")
		}
		for _, entry := range entries {
			name, nok := entry["name"].(string)
			parent, pok := entry["parent_name"].(string)
			path := parent + "/" + name
			if !nok || !pok || name == "" || strings.Contains(name, "/") || !validSitePath(parent) || !validSitePath(path) || seen[path] {
				return nil, errors.New("site requires a unique name and explicit canonical parent_name")
			}
			fields := map[string]any{}
			for key, value := range entry {
				if key == "name" || key == "parent_name" {
					continue
				}
				numeric := false
				allowed := false
				switch kind {
				case "building":
					switch key {
					case "address", "country":
						allowed = true
					case "latitude", "longitude":
						allowed = true
						numeric = true
					}
				case "floor":
					switch key {
					case "rf_model", "units_of_measure":
						allowed = true
					case "floor_number", "width", "length", "height":
						allowed = true
						numeric = true
					}
				}
				if !allowed {
					return nil, errors.New("unsupported site attribute; floor maps and AP placement are not yet supported")
				}
				if numeric {
					var n float64
					switch v := value.(type) {
					case int:
						n = float64(v)
					case float64:
						n = v
					default:
						return nil, errors.New("site numeric field has invalid type")
					}
					if math.IsNaN(n) || math.IsInf(n, 0) {
						return nil, errors.New("site numeric field must be finite")
					}
					if (key == "latitude" && (n < -90 || n > 90)) || (key == "longitude" && (n < -180 || n > 180)) || (key == "floor_number" && math.Trunc(n) != n) || ((key == "width" || key == "length" || key == "height") && n <= 0) {
						return nil, errors.New("site numeric field is outside supported bounds")
					}
					fields[key] = n
				} else {
					v, ok := value.(string)
					if !ok || v == "" {
						return nil, errors.New("site string field must be non-empty")
					}
					if key == "units_of_measure" && v != "feet" && v != "meters" {
						return nil, errors.New("unsupported floor units")
					}
					if key == "rf_model" && v != "Free Space" && v != "Outdoor Open Space" && v != "Cubes And Walled Offices" && v != "Indoor High Ceiling" && v != "Drywall Office Only" {
						return nil, errors.New("unsupported floor RF model")
					}
					fields[key] = v
				}
			}
			if kind == "floor" {
				for _, key := range []string{"width", "length", "height", "rf_model", "units_of_measure"} {
					if _, ok := fields[key]; !ok {
						return nil, errors.New("floor defaults must be explicitly resolved upstream")
					}
				}
			}
			seen[path] = true
			out = append(out, desiredSite{kind, path, fields})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

func planSites(desired []desiredSite, observed []Site) ([]configapi.NetworkControllerDriftEntry, error) {
	actual := map[string]Site{}
	planned := map[string]string{}
	for _, s := range observed {
		actual[s.NameHierarchy] = s
	}
	for _, s := range desired {
		planned[s.path] = s.kind
	}
	drift := []configapi.NetworkControllerDriftEntry{}
	for _, d := range desired {
		parent := parentSitePath(d.path)
		parentKind := planned[parent]
		if parentKind == "" {
			parentKind = actual[parent].Type
		}
		if !validSiteParent(d.kind, parentKind) {
			return nil, errors.New("site parent is absent or has incompatible type")
		}
		a, ok := actual[d.path]
		if !ok {
			drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "sites", Resource: d.path, Change: configapi.NetworkControllerDriftChangeAdd})
			continue
		}
		if a.Type != d.kind {
			return nil, errors.New("existing site type conflicts with intent; replacement is unsupported")
		}
		keys := make([]string, 0, len(d.fields))
		for key := range d.fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			same, err := siteFieldEqual(key, d.fields[key], a, d.fields)
			if err != nil {
				return nil, err
			}
			if !same {
				drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "sites", Resource: d.path, Path: "/" + key, Change: configapi.NetworkControllerDriftChangeUpdate})
			}
		}
	}
	// Existing sites not named by this intent are retained; never synthesize deletes.
	return drift, nil
}
func siteFieldEqual(key string, want any, s Site, fields map[string]any) (bool, error) {
	// Get Sites' _unitsOfMeasure selects a response representation; it is not
	// a persisted floor preference. Compare geometry in the desired units and
	// never schedule a write solely because the read defaulted to feet.
	if key == "units_of_measure" {
		if s.UnitsOfMeasure == nil || (*s.UnitsOfMeasure != "feet" && *s.UnitsOfMeasure != "meters") {
			return false, errors.New("site observation has unsupported units")
		}
		if want != "feet" && want != "meters" {
			return false, errors.New("site intent has unsupported units")
		}
		return true, nil
	}
	stringsByKey := map[string]*string{"address": s.Address, "country": s.Country, "rf_model": s.RFModel, "units_of_measure": s.UnitsOfMeasure}
	if got, ok := stringsByKey[key]; ok {
		if got == nil {
			return false, errors.New("site observation is missing a managed field")
		}
		return *got == want, nil
	}
	numbers := map[string]*float64{"latitude": s.Latitude, "longitude": s.Longitude, "floor_number": s.FloorNumber, "width": s.Width, "length": s.Length, "height": s.Height}
	got := numbers[key]
	if got == nil {
		return false, errors.New("site observation is missing a managed numeric field")
	}
	value := *got
	precision := siteNumericPrecision(key)
	switch key {
	case "width", "length", "height":
		if s.UnitsOfMeasure == nil || (*s.UnitsOfMeasure != "feet" && *s.UnitsOfMeasure != "meters") {
			return false, errors.New("site observation has unsupported units")
		}
		if fields["units_of_measure"] != *s.UnitsOfMeasure {
			if *s.UnitsOfMeasure == "feet" {
				value *= 0.3048
			} else {
				value /= 0.3048
			}
		}
	}
	n, ok := want.(float64)
	if !ok {
		return false, fmt.Errorf("invalid normalized numeric site field")
	}
	return math.Floor(value*precision+0.5) == math.Floor(n*precision+0.5), nil
}

// Match the pinned NetAsCode bulk-site coordinate and geometry precision in
// both submission and comparison. In particular, negative half ties round
// toward positive infinity, as in Terraform's floor(value*precision+0.5).
func siteNumericPrecision(key string) float64 {
	switch key {
	case "latitude", "longitude":
		return 100000
	case "width", "length", "height":
		return 1000
	default:
		return 1
	}
}

func normalizedSiteNumber(key string, value float64) float64 {
	precision := siteNumericPrecision(key)
	return math.Floor(value*precision+0.5) / precision
}
