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

func TestAnycastPoolDependencies(t *testing.T) {
	for _, mode := range []string{"valid", "secondary", "missing", "wrong-site", "ambiguous", "outside-parent", "changed-reservation-id", "changed-prefix", "changed-global-id"} {
		t.Run(mode, func(t *testing.T) {
			pools := fixturePools()
			reservations := fixtureReservations()
			want, err := parseAnycastGateway(map[string]any{"l3_virtual_network": "Corp", "ip_pool_name": "access", "traffic_type": "DATA", "auto_generate_vlan_name": true})
			if err != nil {
				t.Fatal(err)
			}
			if mode == "secondary" {
				bits := 25
				gateway := "192.0.2.1"
				reservations = append(reservations, ipReservation{ID: "secondary", Name: "extra", Type: "LAN", SiteID: "area", V4: &poolAddressSpace{Subnet: "192.0.2.0", PrefixLength: &bits, Gateway: &gateway, GlobalPoolID: "pool"}})
				want.payload["additionalIpPools"] = []map[string]any{{"name": "extra", "order": 2}}
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
				switch r.URL.Path {
				case globalPoolsPath:
					json.NewEncoder(w).Encode(map[string]any{"response": pools})
				case sitePoolsPath:
					json.NewEncoder(w).Encode(map[string]any{"response": reservations})
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			})
			baseline, err := c.anycastPoolDependencies(context.Background(), want, "area")
			if err != nil {
				t.Fatal(err)
			}
			if baseline.primary.ID != "reservation" || baseline.digest == "" {
				t.Fatal("missing binding")
			}
			if mode == "secondary" && (len(baseline.secondary) != 1 || baseline.secondary[0].ID != "secondary") {
				t.Fatal("secondary order lost")
			}
			switch mode {
			case "missing":
				reservations = []ipReservation{}
			case "wrong-site":
				reservations[0].SiteID = "building"
			case "ambiguous":
				r := reservations[0]
				r.ID = "duplicate"
				r.SiteID = "building"
				reservations = append(reservations, r)
			case "outside-parent":
				reservations[0].V4.Subnet = "198.51.100.128"
				g := "198.51.100.129"
				reservations[0].V4.Gateway = &g
			case "changed-reservation-id":
				reservations[0].ID = "replacement"
			case "changed-prefix":
				bits := 26
				reservations[0].V4.PrefixLength = &bits
			case "changed-global-id":
				pools[0].ID = "replacement"
				reservations[0].V4.GlobalPoolID = "replacement"
			}
			got, err := c.anycastPoolDependencies(context.Background(), want, "area")
			invalid := mode == "missing" || mode == "wrong-site" || mode == "ambiguous" || mode == "outside-parent"
			if (err != nil) != invalid {
				t.Fatal("unexpected dependency outcome", err)
			}
			if invalid {
				if got.digest != "" || got.primary.ID != "" {
					t.Fatal("partial invalid binding")
				}
				return
			}
			changed := mode == "changed-reservation-id" || mode == "changed-prefix" || mode == "changed-global-id"
			if (baseline.digest != got.digest) != changed {
				t.Fatal("dependency change not reflected")
			}
		})
	}
}
