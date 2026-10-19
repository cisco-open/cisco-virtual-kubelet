// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestFabricWirelessMulticastCompilerReport(t *testing.T) {
	for _, mode := range []string{"enabled", "disabled", "enable-drift", "disable-drift", "missing-setting", "missing-fabric", "changed-setting", "replaced-fabric", "late-error", "wired-and-wireless", "invalid-setting"} {
		t.Run(mode, func(t *testing.T) {
			desired := mode != "disabled" && mode != "disable-drift"
			multicast := map[string]any{"wireless_multicast_enabled": desired}
			if mode == "wired-and-wireless" {
				multicast["replication_mode"] = "NATIVE_MULTICAST"
			}
			if mode == "invalid-setting" {
				multicast["wireless_multicast_enabled"] = "true"
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"fabric": map[string]any{"fabric_sites": []any{map[string]any{"name": "Global/Lab", "pub_sub_enabled": false, "authentication_template": map[string]any{"name": "No Authentication"}, "multicast": multicast}}}}})
			calls, writes, reads := 0, 0, 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" {
					writes++
					t.Error("unexpected write")
					w.WriteHeader(400)
					return
				}
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case sitesPath:
					reply(sampleSites())
				case fabricSitesPath:
					id := "fabric"
					if mode == "replaced-fabric" && reads > 0 {
						id = "replacement"
					}
					b := false
					rows := []fabricSite{{ID: id, SiteID: "area", Authentication: "No Authentication", PubSub: &b}}
					if mode == "missing-fabric" {
						rows = nil
					}
					reply(rows)
				case fabricMulticastPath:
					reply([]fabricMulticastReplication{{"fabric", "NATIVE_MULTICAST"}})
				case "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast", "/dna/intent/api/v1/sda/fabrics/replacement/wirelessMulticast":
					reads++
					if mode == "late-error" && reads > 1 {
						w.WriteHeader(500)
						return
					}
					if mode == "missing-setting" {
						reply(map[string]any{})
						return
					}
					enabled := desired
					if mode == "enable-drift" || mode == "disable-drift" || mode == "wired-and-wireless" || mode == "changed-setting" && reads > 1 {
						enabled = !desired
					}
					reply(map[string]any{"multicastEnabled": enabled})
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			plan, err := c.compileFabricConfiguration(raw)
			if mode == "invalid-setting" {
				if err == nil || plan != nil || calls != 0 {
					t.Fatal("invalid model admitted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expectedCount := 2
			if mode == "wired-and-wireless" {
				expectedCount++
			}
			if plan.count() != expectedCount {
				t.Fatal("setting missing from count", plan.count())
			}
			ctx := context.Background()
			write, err := plan.next(ctx)
			if !errors.Is(err, errConfigurationDeviceAdmission) || write != nil || calls != 0 {
				t.Fatal("Apply gate bypassed", err, calls)
			}
			drift, err := plan.report(ctx)
			failed := mode == "missing-setting" || mode == "missing-fabric" || mode == "changed-setting" || mode == "replaced-fabric" || mode == "late-error"
			if failed {
				if err == nil || drift != nil {
					t.Fatal("unknown or unstable state reported as compliance", drift, err)
				}
			} else {
				if err != nil || reads != 2 {
					t.Fatal("report", err, reads)
				}
				expected := 0
				if mode == "enable-drift" || mode == "disable-drift" || mode == "wired-and-wireless" {
					expected = 1
				}
				if len(drift) != expected {
					t.Fatal("incorrect drift", drift)
				}
				if expected == 1 && (string(drift[0].Change) != "Update" || drift[0].Path != "/fabric_sites/multicast/wireless_multicast_enabled") {
					t.Fatal("incorrect change", drift)
				}
			}
			if writes != 0 {
				t.Fatal("report mutated controller")
			}
		})
	}
}
