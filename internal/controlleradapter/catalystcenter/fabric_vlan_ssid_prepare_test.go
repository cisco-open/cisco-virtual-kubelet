// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestFabricVLANSSIDDeclaredPrerequisites(t *testing.T) {
	for _, mode := range []string{"ready", "base-drift", "multicast-drift", "multicast-matched", "missing-path", "empty", "unknown-model", "read-error"} {
		t.Run(mode, func(t *testing.T) {
			ssids := []any{map[string]any{"name": "Staff", "vlan_name": "Clients"}}
			if mode == "empty" {
				ssids = []any{}
			}
			site := map[string]any{"name": "Global/Lab", "pub_sub_enabled": false, "authentication_template": map[string]any{"name": "No Authentication"}, "wireless_ssids": ssids}
			if mode == "multicast-drift" || mode == "multicast-matched" {
				site["multicast"] = map[string]any{"wireless_multicast_enabled": true}
			}
			if mode == "unknown-model" {
				site["unconsumed"] = true
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"fabric_sites": []any{site}}}})
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" {
					t.Error("prerequisite check mutated controller")
					w.WriteHeader(400)
					return
				}
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case sitesPath:
					if mode == "read-error" {
						w.WriteHeader(500)
						return
					}
					reply(sampleSites())
				case fabricSitesPath:
					b := mode == "base-drift"
					reply([]fabricSite{{ID: "fabric", SiteID: "area", Authentication: "No Authentication", PubSub: &b}})
				case "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast":
					reply(map[string]bool{"multicastEnabled": mode == "multicast-matched"})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			a := &adapter{client: c}
			path := "Global/Lab"
			if mode == "missing-path" {
				path = "Global/Other"
			}
			want, err := a.declaredFabricVLANSSIDs(context.Background(), &configSourceSnapshot{raw: raw}, path)
			valid := mode == "ready" || mode == "multicast-matched" || mode == "multicast-drift"
			if (err == nil) != valid {
				t.Fatal("prerequisite result", err)
			}
			if !valid {
				if want != nil {
					t.Fatal("partial declaration escaped")
				}
				return
			}
			if calls == 0 || len(want) != 1 || want[0].VLAN != "Clients" || want[0].SSIDs[0].Name != "Staff" {
				t.Fatal("declaration lost", want)
			}
		})
	}
}
