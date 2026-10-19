// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestAnycastPoolPreviewDistinguishesDeclaredPrerequisites(t *testing.T) {
	for _, mode := range []string{"observed", "declared-reservation", "declared-parent", "undeclared", "wrong-owner", "changed-reservation", "changed-parent", "observed-with-parent-change", "overlap", "missing-site", "secondary-missing", "secondary-duplicate", "secondary-declared", "secondary-wrong-owner"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			intent, err := parseNetworkReportIntent([]byte(poolModel))
			if err != nil {
				t.Fatal(err)
			}
			pools, reservations, sites := fixturePools(), fixtureReservations(), sampleSites()
			want, err := parseAnycastGateway(map[string]any{"ip_pool_name": "access", "l3_virtual_network": "Corp", "traffic_type": "DATA", "auto_generate_vlan_name": true})
			if err != nil {
				t.Fatal(err)
			}
			owner := "area"
			switch mode {
			case "declared-reservation":
				reservations = []ipReservation{}
			case "declared-parent":
				pools = []ipPool{}
				reservations = []ipReservation{}
			case "undeclared":
				intent.reservations = nil
				reservations = []ipReservation{}
			case "wrong-owner":
				owner = "building"
			case "changed-reservation":
				gateway := "192.0.2.130"
				intent.reservations[0].V4.Gateway = &gateway
			case "changed-parent", "observed-with-parent-change":
				gateway := "192.0.2.2"
				intent.pools[0].Space.Gateway = &gateway
				if mode == "observed-with-parent-change" {
					intent.reservations = nil
				}
			case "overlap":
				other := fixturePools()[0]
				other.ID = "other"
				other.Name = "other"
				pools = append(pools, other)
			case "missing-site":
				sites = []Site{}
			case "secondary-declared", "secondary-wrong-owner":
				bits := 26
				gateway := "192.0.2.1"
				extra := desiredReservation{ipReservation: ipReservation{Name: "extra", Type: "LAN", V4: &poolAddressSpace{Subnet: "192.0.2.0", PrefixLength: &bits, Gateway: &gateway, GlobalPoolID: "parent"}}, site: "Global/Lab", siteKind: "area"}
				if mode == "secondary-wrong-owner" {
					extra.site = "Global/Lab/Building"
					extra.siteKind = "building"
				}
				intent.reservations = append(intent.reservations, extra)
				want.payload["additionalIpPools"] = []map[string]any{{"name": "extra", "order": 2}}
			case "secondary-missing":
				want.payload["additionalIpPools"] = []map[string]any{{"name": "absent", "order": 2}}
			case "secondary-duplicate":
				want.payload["additionalIpPools"] = []map[string]any{{"name": "access", "order": 2}}
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("preview mutated appliance")
					w.WriteHeader(400)
					return
				}
				var result any
				switch r.URL.Path {
				case globalPoolsPath:
					result = pools
				case sitePoolsPath:
					result = reservations
				case sitesPath:
					result = sites
				default:
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": result})
			})
			e := &ipamConfigurationExecutor{client: c, intent: intent}
			preview, err := e.previewAnycastPools(ctx, want, owner)
			pending := mode == "declared-reservation" || mode == "declared-parent" || mode == "changed-reservation" || mode == "changed-parent" || mode == "observed-with-parent-change" || mode == "secondary-declared"
			valid := mode == "observed" || pending
			if (err == nil) != valid || (err == nil && preview.pending != pending) {
				t.Fatal("incorrect preview", preview, err)
			}
			if !valid && preview.pending {
				t.Fatal("invalid graph returned partial readiness")
			}
			if mode == "declared-reservation" || mode == "declared-parent" {
				if _, err := c.anycastPoolDependencies(ctx, want, owner); err == nil {
					t.Fatal("declaration supplied native submission authority")
				}
			}
		})
	}
}
