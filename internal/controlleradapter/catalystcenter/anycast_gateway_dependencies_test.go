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

func TestAnycastGatewayFabricDependencies(t *testing.T) {
	for _, mode := range []string{"site", "zone", "anchored-site", "missing-membership", "missing-parent", "parent-drift", "wrong-reservation-owner", "changed-parent-id", "changed-vn-id"} {
		t.Run(mode, func(t *testing.T) {
			b := false
			sites := []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &b}}
			zones := []fabricZone{{ID: "zone", SiteID: "building", Authentication: "No Authentication"}}
			network := fabricL3Network{ID: "network", Name: "Corp", Fabrics: []string{"fabric", "zone"}}
			reservations := fixtureReservations()
			parent := anycastObservationFixture()
			parent["id"] = "parent-gateway"
			parent["ipPoolName"] = "access"
			parent["vlanName"] = "192.0.2.128/25-Corp"
			gateways := []any{parent}
			wantModel := map[string]any{"ip_pool_name": "access", "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": true, "critical_pool": false}
			intent, err := parseAnycastGateway(wantModel)
			if err != nil {
				t.Fatal(err)
			}
			want := fabricAnycastIntent{path: "Global/Lab/Building", parent: "Global/Lab", anycastGatewayIntent: intent}
			if mode == "site" {
				want.path = "Global/Lab"
				want.parent = ""
				gateways = []any{}
			}
			if mode == "anchored-site" {
				want.parent = ""
				sites = append(sites, fabricSite{ID: "extended", SiteID: "building", Authentication: "Closed Authentication", PubSub: &b})
				zones = []fabricZone{}
				network.Anchor = "fabric"
				network.Fabrics = []string{"fabric", "extended"}
			}
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
				var response any
				switch r.URL.Path {
				case sitesPath:
					response = sampleSites()
				case fabricSitesPath:
					response = sites
				case fabricZonesPath:
					response = zones
				case fabricL3NetworksPath:
					response = []fabricL3Network{network}
				case globalPoolsPath:
					response = fixturePools()
				case sitePoolsPath:
					response = reservations
				case fabricL2NetworksPath:
					response = []fabricL2Network{}
				case anycastGatewaysPath:
					response = gateways
				default:
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": response})
			})
			baseline, err := c.anycastGatewayDependencies(context.Background(), want)
			if err != nil {
				t.Fatal("initial dependencies", err)
			}
			if baseline.reservationSiteID != "area" || baseline.pools.primary.ID != "reservation" || baseline.digest == "" {
				t.Fatal("wrong pool owner")
			}
			if mode != "site" && (baseline.parent == nil || baseline.parent.ID != "parent-gateway") {
				t.Fatal("missing inherited parent")
			}
			if mode == "anchored-site" && baseline.fabricID != "extended" {
				t.Fatal("anchor replaced target fabric")
			}
			switch mode {
			case "missing-membership":
				network.Fabrics = []string{"fabric"}
			case "missing-parent":
				gateways = []any{}
			case "parent-drift":
				parent["isCriticalPool"] = true
			case "wrong-reservation-owner":
				reservations[0].SiteID = "building"
			case "changed-parent-id":
				parent["id"] = "replacement"
			case "changed-vn-id":
				network.ID = "replacement"
			}
			got, err := c.anycastGatewayDependencies(context.Background(), want)
			invalid := mode == "missing-membership" || mode == "missing-parent" || mode == "parent-drift" || mode == "wrong-reservation-owner"
			if (err != nil) != invalid {
				t.Fatal("unexpected dependency result", err)
			}
			if invalid {
				if got.digest != "" || got.fabricID != "" {
					t.Fatal("partial invalid dependencies")
				}
				return
			}
			changed := mode == "changed-parent-id" || mode == "changed-vn-id"
			if (got.digest != baseline.digest) != changed {
				t.Fatal("dependency identity change lost")
			}
		})
	}
}
