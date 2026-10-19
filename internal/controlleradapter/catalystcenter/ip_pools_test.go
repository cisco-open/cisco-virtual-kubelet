// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const poolModel = `{"catalyst_center":{"sites":{"areas":[{"name":"Lab","parent_name":"Global","ip_pools_reservations":["access"]}]},"network_settings":{"ip_pools":[{"name":"parent","type":"Generic","ip_address_space":"IPv4","ip_pool_cidr":"192.0.2.0/24","gateway":"192.0.2.1","dhcp_servers":[],"dns_servers":["192.0.2.53"],"ip_pools_reservations":[{"name":"access","type":"LAN","subnet":"192.0.2.128","prefix_length":25,"gateway":"192.0.2.129"}]}]}}}`

func fixturePools() []ipPool {
	bits := 24
	gateway := "192.0.2.1"
	dhcp := []string{}
	dns := []string{"192.0.2.53"}
	return []ipPool{{ID: "pool", Name: "parent", Type: "Generic", Space: &poolAddressSpace{Subnet: "192.0.2.0", PrefixLength: &bits, Gateway: &gateway, DHCP: &dhcp, DNS: &dns}}}
}
func fixtureReservations() []ipReservation {
	bits := 25
	gateway := "192.0.2.129"
	return []ipReservation{{ID: "reservation", Name: "access", Type: "LAN", SiteID: "area", V4: &poolAddressSpace{Subnet: "192.0.2.128", PrefixLength: &bits, Gateway: &gateway, GlobalPoolID: "pool"}}}
}
func TestIPAMReport(t *testing.T) {
	for _, mode := range []string{"match", "missing-pool", "missing-reservation", "changed", "missing-field", "wrong-parent", "duplicate-name", "partial"} {
		t.Run(mode, func(t *testing.T) {
			intent, err := parseNetworkReportIntent([]byte(poolModel))
			if err != nil {
				t.Fatal(err)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
				}
				switch r.URL.Path {
				case globalPoolsPath:
					p := fixturePools()
					if mode == "missing-pool" {
						p = []ipPool{}
					}
					if mode == "missing-field" {
						p[0].Space.DNS = nil
					}
					if mode == "duplicate-name" {
						q := p[0]
						q.ID = "another"
						p = append(p, q)
					}
					if mode == "changed" {
						s := "192.0.2.2"
						p[0].Space.Gateway = &s
					}
					if mode == "partial" {
						w.WriteHeader(206)
					}
					json.NewEncoder(w).Encode(map[string]any{"response": p})
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case sitePoolsPath:
					p := fixtureReservations()
					if mode == "missing-reservation" || mode == "missing-pool" {
						p = []ipReservation{}
					}
					if mode == "wrong-parent" {
						p[0].V4.GlobalPoolID = "unknown"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": p})
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			})
			drift, err := c.reportIPAM(context.Background(), intent)
			valid := mode == "match" || mode == "missing-pool" || mode == "missing-reservation" || mode == "changed"
			if (err == nil) != valid {
				t.Fatalf("valid=%v err=%v", valid, err)
			}
			if !valid {
				if len(drift) != 0 {
					t.Fatal("partial drift published")
				}
				return
			}
			want := 1
			if mode == "match" {
				want = 0
			}
			if mode == "missing-pool" {
				want = 2
			}
			if len(drift) != want {
				t.Fatalf("drift=%+v", drift)
			}
			raw, _ := json.Marshal(drift)
			if strings.Contains(string(raw), "192.0.2") {
				t.Fatal("values leaked")
			}
		})
	}
}
func TestIPAMIntentValidation(t *testing.T) {
	for _, model := range []string{
		strings.Replace(poolModel, `192.0.2.0/24`, `192.0.2.1/24`, 1),
		strings.Replace(poolModel, `"ip_address_space":"IPv4"`, `"ip_address_space":"IPv6"`, 1),
		strings.Replace(poolModel, `"subnet":"192.0.2.128"`, `"subnet":"198.51.100.128"`, 1),
		strings.Replace(poolModel, `"gateway":"192.0.2.129"`, `"gateway":"192.0.2.1"`, 1),
		strings.Replace(poolModel, `"ip_pools_reservations":["access"]`, `"ip_pools_reservations":["absent"]`, 1),
		strings.Replace(poolModel, `"ip_pools_reservations":["access"]`, `"ip_pools_reservations":["access","access"]`, 1),
		strings.Replace(poolModel, `"ip_pools_reservations":["access"]`, `"ip_pools_reservations":[]`, 1),
		strings.Replace(poolModel, `"dhcp_servers":[]`, `"dhcp_servers":null`, 1),
		strings.Replace(poolModel, `"prefix_length":25`, `"prefix_length":25.5`, 1),
	} {
		if _, err := parseNetworkReportIntent([]byte(model)); err == nil {
			t.Fatal("invalid pool intent accepted")
		}
	}
}
func TestIPAMDualStack(t *testing.T) {
	var model map[string]any
	if json.Unmarshal([]byte(poolModel), &model) != nil {
		t.Fatal("fixture")
	}
	root := model["catalyst_center"].(map[string]any)
	network := root["network_settings"].(map[string]any)
	network["ip_pools"] = append(network["ip_pools"].([]any), map[string]any{"name": "parent6", "type": "Generic", "ip_address_space": "IPv6", "ip_pool_cidr": "2001:db8::/48", "ip_pools_reservations": []any{map[string]any{"name": "access", "type": "LAN", "subnet": "2001:db8:0:1::", "prefix_length": 64}}})
	raw, _ := json.Marshal(model)
	intent, err := parseNetworkReportIntent(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(intent.pools) != 2 || len(intent.reservations) != 1 || intent.reservations[0].V6 == nil || intent.reservations[0].V6.SLAAC == nil || !*intent.reservations[0].V6.SLAAC {
		t.Fatal("dual stack merge failed")
	}
}
