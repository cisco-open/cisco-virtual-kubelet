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

func TestMulticastSnapshot(t *testing.T) {
	for _, mode := range []string{"valid", "missing-membership", "third-rp", "wrong-role", "changed-serial", "changed-pool", "changed-member", "changed-vn", "retained-replaced-serial", "reordered-memberships", "non-rp-replaced", "non-rp-missing-serial", "non-rp-role-changed"} {
		t.Run(mode, func(t *testing.T) {
			b := false
			pools := fixturePools()
			reservations := fixtureReservations()
			networks := []fabricL3Network{{ID: "vn", Name: "Corp", Fabrics: []string{"fabric"}}}
			if mode == "reordered-memberships" {
				networks[0].Fabrics = []string{"fabric", "other-fabric"}
			}
			devices := []Device{{ID: "a", Hostname: "border-a.lab", Serial: "SERIAL-A"}, {ID: "b", Hostname: "border-b.lab", Serial: "SERIAL-B"}, {ID: "c", Hostname: "border-c.lab", Serial: "SERIAL-C"}}
			members := []fabricDeviceMembership{}
			for _, id := range []string{"a", "b", "c"} {
				members = append(members, fabricDeviceMembership{ID: "member-" + id, NetworkDeviceID: id, FabricID: "fabric", Roles: []string{"BORDER_NODE"}})
			}
			native := []map[string]any{multicastSiteRow(t, "Other", "access", "b").fields}
			if mode == "missing-membership" {
				networks[0].Fabrics = []string{}
			}
			if mode == "third-rp" {
				native = append(native, multicastSiteRow(t, "Third", "access", "c").fields)
			}
			if mode == "non-rp-missing-serial" {
				devices[2].Serial = ""
			}
			if mode == "wrong-role" {
				members[1].Roles = []string{"EDGE_NODE"}
			}
			want, err := parseFabricMulticastVN(map[string]any{"name": "Corp", "ip_pool_name": "access", "multicast_rps": []any{map[string]any{"rp_location": "FABRIC", "fabric_rps": []any{"border-a"}}}})
			if err != nil {
				t.Fatal(err)
			}
			reads := map[string]int{}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				reads[r.URL.Path]++
				var value any
				switch r.URL.Path {
				case sitesPath:
					value = sampleSites()
				case fabricSitesPath:
					value = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "No Authentication", PubSub: &b}}
				case fabricL3NetworksPath:
					value = networks
				case fabricMulticastVNsPath:
					value = native
				case globalPoolsPath:
					value = pools
				case sitePoolsPath:
					value = reservations
				case devicesPath:
					value = devices
				case fabricDevicesPath:
					value = members
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": value})
			})
			baseline, err := c.multicastSnapshot(context.Background(), "Global/Lab", []fabricMulticastVNIntent{want})
			invalid := mode == "missing-membership" || mode == "third-rp" || mode == "wrong-role" || mode == "non-rp-missing-serial"
			if (err != nil) != invalid {
				t.Fatal("snapshot", err)
			}
			if invalid {
				if baseline != nil {
					t.Fatal("partial snapshot")
				}
				return
			}
			if baseline.digest == "" || baseline.fabricID != "fabric" || len(baseline.devices) != 3 || baseline.bodies["Corp"]["id"] != nil {
				t.Fatal("missing bindings or synthetic ID in payload")
			}
			for endpoint, count := range reads {
				if count != 1 {
					t.Fatal("repeated collection", endpoint, count)
				}
			}
			switch mode {
			case "reordered-memberships":
				networks[0].Fabrics = []string{"other-fabric", "fabric"}
			case "non-rp-replaced":
				devices[2].Serial = "replacement"
			case "non-rp-role-changed":
				members[2].Roles = []string{"EDGE_NODE"}
			case "changed-serial":
				devices[0].Serial = "replacement"
			case "changed-pool":
				reservations[0].ID = "replacement"
			case "changed-member":
				members[0].ID = "replacement"
			case "changed-vn":
				networks[0].ID = "replacement"
			case "retained-replaced-serial":
				devices[1].Serial = "replacement"
			}
			latest, err := c.multicastSnapshot(context.Background(), "Global/Lab", []fabricMulticastVNIntent{want})
			if err != nil {
				t.Fatal("refresh", err)
			}
			if (latest.digest != baseline.digest) != (mode != "valid" && mode != "reordered-memberships") {
				t.Fatal("replacement evidence lost")
			}
		})
	}
}
