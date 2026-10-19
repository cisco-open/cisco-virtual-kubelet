// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFabricZoneNestedModel(t *testing.T) {
	for _, mode := range []string{"valid", "no-zones", "empty-zones", "wrong-parent", "equal-parent", "duplicate", "nested-zone", "unknown", "missing-auth", "invalid-auth", "invalid-site", "site-collision", "intermediate-site", "null"} {
		t.Run(mode, func(t *testing.T) {
			zone := map[string]any{"name": "Global/Lab/Building", "authentication_template": map[string]any{"name": "No Authentication"}}
			site := map[string]any{"name": "Global/Lab", "pub_sub_enabled": false, "authentication_template": map[string]any{"name": "Closed Authentication"}, "fabric_zones": []any{zone}}
			value := []any{site}
			switch mode {
			case "no-zones":
				delete(site, "fabric_zones")
			case "empty-zones":
				site["fabric_zones"] = []any{}
			case "wrong-parent":
				zone["name"] = "Global/Other/Building"
			case "equal-parent":
				zone["name"] = "Global/Lab"
			case "duplicate":
				site["fabric_zones"] = []any{zone, zone}
			case "nested-zone":
				site["fabric_zones"] = []any{zone, map[string]any{"name": "Global/Lab/Building/Floor", "authentication_template": map[string]any{"name": "No Authentication"}}}
			case "unknown":
				zone["anycast_gateways"] = []any{}
			case "missing-auth":
				delete(zone, "authentication_template")
			case "invalid-auth":
				zone["authentication_template"] = map[string]any{"name": "Unknown"}
			case "invalid-site":
				site["pub_sub_enabled"] = nil
			case "site-collision":
				value = append(value, map[string]any{"name": "Global/Lab/Building", "pub_sub_enabled": false, "authentication_template": map[string]any{"name": "Closed Authentication"}})
			case "intermediate-site":
				zone["name"] = "Global/Lab/Building/Floor"
				value = append(value, map[string]any{"name": "Global/Lab/Building", "pub_sub_enabled": false, "authentication_template": map[string]any{"name": "Closed Authentication"}})
			case "null":
				site["fabric_zones"] = nil
			}
			before, _ := json.Marshal(value)
			sites, zones, err := parseFabricSitesWithZones(value)
			valid := mode == "valid" || mode == "no-zones" || mode == "empty-zones"
			if (err == nil) != valid {
				t.Fatal("unexpected nested model outcome", err)
			}
			if !valid && (sites != nil || zones != nil) {
				t.Fatal("invalid nested model returned partial intent")
			}
			after, _ := json.Marshal(value)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source mutated")
			}
			if mode == "valid" && (len(sites) != 1 || len(zones) != 1 || zones[0].parent != "Global/Lab" || zones[0].authentication != "No Authentication" || sites[0].authentication != "Closed Authentication") {
				t.Fatal("parent or authentication identity lost")
			}
			if mode == "valid" {
				if _, err := parseFabricSiteModels(value); err == nil {
					t.Fatal("ordinary site parser silently discarded zones")
				}
			}
		})
	}
}
