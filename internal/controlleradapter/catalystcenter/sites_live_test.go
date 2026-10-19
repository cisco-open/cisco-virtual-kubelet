// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// TestLiveCatalystCenterSitesReport only authenticates and reads Site Design.
// Synthetic drift changes local intent, never controller configuration.
func TestLiveCatalystCenterSitesReport(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	actual, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	groups := map[string][]map[string]any{"areas": {}, "buildings": {}, "floors": {}}
	for _, site := range actual {
		if site.Type == "global" {
			continue
		}
		item := map[string]any{"name": site.Name, "parent_name": parentSitePath(site.NameHierarchy)}
		if site.Type == "building" {
			for key, value := range map[string]*string{"address": site.Address, "country": site.Country} {
				if value != nil && *value != "" {
					item[key] = *value
				}
			}
			for key, value := range map[string]*float64{"latitude": site.Latitude, "longitude": site.Longitude} {
				if value != nil {
					item[key] = *value
				}
			}
		}
		if site.Type == "floor" {
			for key, value := range map[string]*string{"rf_model": site.RFModel, "units_of_measure": site.UnitsOfMeasure} {
				if value != nil {
					item[key] = *value
				}
			}
			for key, value := range map[string]*float64{"width": site.Width, "length": site.Length, "height": site.Height, "floor_number": site.FloorNumber} {
				if value != nil {
					item[key] = *value
				}
			}
		}
		key := site.Type + "s"
		groups[key] = append(groups[key], item)
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": groups}})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := parseSites(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(desired) == 0 {
		t.Fatal("lab has no non-global sites to qualify")
	}
	drift, err := planSites(desired, actual)
	if err != nil || len(drift) != 0 {
		t.Fatalf("round trip failed: error=%v count=%d", err, len(drift))
	}
	synthetic := "Global/CVK-ReadOnly-Qualification-20990101"
	for _, site := range actual {
		if site.NameHierarchy == synthetic {
			t.Fatal("synthetic test identity already exists")
		}
	}
	desired = append(desired, desiredSite{kind: "area", path: synthetic, fields: map[string]any{}})
	drift, err = planSites(desired, actual)
	if err != nil || len(drift) != 1 || string(drift[0].Change) != "Add" {
		t.Fatal("synthetic missing-site difference failed")
	}
	if len(groups["buildings"]) > 0 {
		groups["buildings"][0]["address"] = "CVK synthetic local-only drift"
		raw, _ = json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": groups}})
		desired, err = parseSites(raw)
		if err != nil {
			t.Fatal(err)
		}
		drift, err = planSites(desired, actual)
		if err != nil || len(drift) != 1 || drift[0].Path != "/address" {
			t.Fatal("synthetic changed-attribute difference failed")
		}
	}
	t.Logf("PASS: live sites=%d areas=%d buildings=%d floors=%d; round-trip and synthetic drift; no configuration writes", len(actual), len(groups["areas"]), len(groups["buildings"]), len(groups["floors"]))
}
