// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFabricL3AssociationNestedModel(t *testing.T) {
	for _, mode := range []string{"valid", "site-object", "duplicate", "zone-object", "null", "unknown-site", "unknown-zone", "bad-site-object", "wrong-parent", "bad-l2"} {
		t.Run(mode, func(t *testing.T) {
			var root map[string]any
			if err := json.Unmarshal([]byte(fabricL2Fixture), &root); err != nil {
				t.Fatal(err)
			}
			value := root["catalyst_center"].(map[string]any)["fabric"].(map[string]any)["fabric_sites"].([]any)
			site := value[0].(map[string]any)
			zone := site["fabric_zones"].([]any)[0].(map[string]any)
			site["l3_virtual_networks"] = []any{"Corp"}
			zone["l3_virtual_networks"] = []any{"Corp"}
			switch mode {
			case "site-object":
				site["l3_virtual_networks"] = []any{map[string]any{"name": "Corp"}}
			case "duplicate":
				site["l3_virtual_networks"] = []any{"Corp", map[string]any{"name": "Corp"}}
			case "zone-object":
				zone["l3_virtual_networks"] = []any{map[string]any{"name": "Corp"}}
			case "null":
				site["l3_virtual_networks"] = nil
			case "unknown-site":
				site["anycast_gateways"] = []any{}
			case "unknown-zone":
				zone["anycast_gateways"] = []any{}
			case "bad-site-object":
				site["l3_virtual_networks"] = []any{map[string]any{"name": "Corp", "anchor_site": "Global/Elsewhere"}}
			case "wrong-parent":
				zone["name"] = "Global/Other/Building"
			case "bad-l2":
				zone["l2_virtual_networks"] = []any{"Missing"}
			}
			before, _ := json.Marshal(value)
			sites, zones, l2, associations, err := parseFabricSitesWithL3Associations(value)
			valid := mode == "valid" || mode == "site-object"
			if (err == nil) != valid {
				t.Fatal("unexpected parse result", err)
			}
			if !valid && (sites != nil || zones != nil || l2 != nil || associations != nil) {
				t.Fatal("invalid partial intent")
			}
			after, _ := json.Marshal(value)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("source mutated")
			}
			if valid && (len(sites) != 1 || len(zones) != 1 || len(l2) != 2 || len(associations) != 2 || associations[0].path != "Global/Lab" || associations[0].parent != "" || associations[1].parent != "Global/Lab" || associations[1].name != "Corp") {
				t.Fatal("lost dependency intent")
			}
		})
	}
}
