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

func TestMulticastPoolDependencies(t *testing.T) {
	for _, mode := range []string{"v4", "dual", "v6-on-v4", "dual-two-rps", "retained-v6-on-v4", "retained-dual-two-rps", "wrong-site", "missing-pool", "replacement", "changed-v6-prefix", "unknown-observation"} {
		t.Run(mode, func(t *testing.T) {
			pools := fixturePools()
			reservations := fixtureReservations()
			dual := mode == "dual" || mode == "dual-two-rps" || mode == "retained-dual-two-rps" || mode == "changed-v6-prefix"
			if dual {
				globalBits := 48
				localBits := 64
				gateway := "2001:db8:1::1"
				pools = append(pools, ipPool{ID: "pool-v6", Name: "parent-v6", Type: "Generic", Space: &poolAddressSpace{Subnet: "2001:db8:1::", PrefixLength: &globalBits}})
				reservations[0].V6 = &poolAddressSpace{Subnet: "2001:db8:1::", PrefixLength: &localBits, Gateway: &gateway, GlobalPoolID: "pool-v6"}
			}
			rp := map[string]any{"rp_location": "FABRIC", "fabric_rps": []any{"border-a"}}
			if mode == "dual" || mode == "v6-on-v4" {
				rp["ipv6_asm_ranges"] = []any{"ff3e::/96"}
			}
			if mode == "dual-two-rps" {
				rp["fabric_rps"] = []any{"border-a", "border-b"}
			}
			want, err := parseFabricMulticastVN(map[string]any{"name": "Corp", "ip_pool_name": "access", "multicast_rps": []any{rp}})
			if err != nil {
				t.Fatal(err)
			}
			var observed *fabricMulticastVN
			if mode == "retained-v6-on-v4" || mode == "retained-dual-two-rps" || mode == "unknown-observation" {
				native := multicastObservationFixture()
				native["ipPoolName"] = "access"
				if mode == "unknown-observation" {
					native["futureField"] = true
				}
				raw, _ := json.Marshal(native)
				row, err := parseFabricMulticastVNObservation(raw)
				if err != nil {
					t.Fatal(err)
				}
				observed = &row
				// Omission must not bypass validation of retained native RP settings.
				want.rps = nil
				want.rpsDeclared = false
			}
			if mode == "wrong-site" {
				reservations[0].SiteID = "building"
			}
			if mode == "missing-pool" {
				reservations = []ipReservation{}
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
				case globalPoolsPath:
					response = pools
				case sitePoolsPath:
					response = reservations
				default:
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": response})
			})
			baseline, err := c.multicastPoolDependencies(context.Background(), want, "area", observed)
			valid := mode == "v4" || mode == "dual" || mode == "replacement" || mode == "changed-v6-prefix"
			if (err == nil) != valid {
				t.Fatal("unexpected binding", err)
			}
			if !valid {
				if baseline.digest != "" || baseline.primary.ID != "" {
					t.Fatal("partial invalid binding")
				}
				return
			}
			if baseline.primary.ID != "reservation" || baseline.digest == "" {
				t.Fatal("missing evidence")
			}
			if mode == "replacement" {
				reservations[0].ID = "replacement"
			}
			if mode == "changed-v6-prefix" {
				bits := 65
				reservations[0].V6.PrefixLength = &bits
			}
			latest, err := c.multicastPoolDependencies(context.Background(), want, "area", observed)
			if err != nil {
				t.Fatal("refresh", err)
			}
			changed := mode == "replacement" || mode == "changed-v6-prefix"
			if (latest.digest != baseline.digest) != changed {
				t.Fatal("dependency identity/content change lost")
			}
		})
	}
}
