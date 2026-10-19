// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestFabricPendingEventsModel(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "absent", "multiple", "zone", "multicast", "null", "string", "number", "unknown", "injected", "duplicate", "invalid-path", "invalid-base", "unsupported-wireless"} {
		t.Run(mode, func(t *testing.T) {
			site := map[string]any{"name": "Global/Lab/Building", "pub_sub_enabled": true, "authentication_template": map[string]any{"name": "Closed Authentication"}, "reconfigure": true}
			rows := []any{site}
			switch mode {
			case "disabled":
				site["reconfigure"] = false
			case "absent":
				delete(site, "reconfigure")
			case "null":
				site["reconfigure"] = nil
			case "string":
				site["reconfigure"] = "true"
			case "number":
				site["reconfigure"] = 1
			case "unknown":
				site["unexpected"] = true
			case "injected":
				site["eventId"] = "foreign"
			case "duplicate":
				rows = append(rows, site)
			case "invalid-path":
				site["name"] = "Global"
			case "invalid-base":
				site["pub_sub_enabled"] = "true"
			case "multiple":
				rows = append(rows, map[string]any{"name": "Global/Another/Building", "pub_sub_enabled": false, "authentication_template": map[string]any{"name": "Closed Authentication"}, "reconfigure": true})
			case "zone":
				site["fabric_zones"] = []any{map[string]any{"name": "Global/Lab/Building/Floor", "authentication_template": map[string]any{"name": "Closed Authentication"}}}
			case "multicast":
				site["multicast"] = map[string]any{"replication_mode": "NATIVE_MULTICAST"}
			case "unsupported-wireless":
				site["multicast"] = map[string]any{"wireless_multicast_enabled": true}
			}
			before, _ := json.Marshal(rows)
			model, multicast, wants, err := parseFabricSitesWithPendingEvents(rows)
			valid := mode == "enabled" || mode == "disabled" || mode == "absent" || mode == "multiple" || mode == "zone" || mode == "multicast"
			if (err == nil) != valid {
				t.Fatal("model result", err)
			}
			after, _ := json.Marshal(rows)
			if string(before) != string(after) {
				t.Fatal("caller model changed")
			}
			if !valid {
				return
			}
			count := 1
			if mode == "multiple" {
				count = 2
			}
			if len(model.sites) != count {
				t.Fatal("lost foundation sites")
			}
			if mode == "disabled" || mode == "absent" {
				count = 0
			}
			if len(wants) != count {
				t.Fatal("incorrect event intent count")
			}
			if mode == "multiple" && wants[0].path != "Global/Another/Building" {
				t.Fatal("unstable ordering")
			}
			if mode == "zone" && len(model.zones) != 1 {
				t.Fatal("lost zone intent")
			}
			if mode == "multicast" && len(multicast) != 1 {
				t.Fatal("lost multicast intent")
			}
			// Public parsing remains fail-closed until a runtime owns this effect.
			if mode != "absent" {
				if _, _, err := parseFabricSitesWithMulticast(rows); err == nil {
					t.Fatal("public parser discarded reconfigure")
				}
			}
		})
	}
}
