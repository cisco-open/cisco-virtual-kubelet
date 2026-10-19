// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestIPAMSubmissionRevalidatesNativePlan(t *testing.T) {
	for _, mode := range []string{"pool-stable", "pool-replaced", "pool-preserved-field", "pool-converged", "pool-created-late", "reservation-stable", "reservation-replaced", "parent-replaced", "site-replaced", "reservation-preserved-field", "reservation-converged"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			intent, err := parseNetworkReportIntent([]byte(poolModel))
			if err != nil {
				t.Fatal(err)
			}
			pools, reservations, sites := fixturePools(), fixtureReservations(), sampleSites()
			reservation := !strings.HasPrefix(mode, "pool-")
			desiredGateway := "192.0.2.2"
			if reservation {
				desiredGateway = "192.0.2.130"
				intent.reservations[0].V4.Gateway = &desiredGateway
				dns := []string{"192.0.2.53"}
				reservations[0].V4.DNS = &dns
			} else {
				intent.reservations = nil
				reservations = []ipReservation{}
				intent.pools[0].Space.Gateway = &desiredGateway
				intent.pools[0].Space.DNS = nil
			}
			if mode == "pool-created-late" {
				pools = []ipPool{}
			}
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					writes++
					path, field := globalPoolsPath+"/pool", "addressSpace"
					if reservation {
						path, field = sitePoolsPath+"/reservation", "ipV4AddressSpace"
					}
					if r.Method != "PUT" || r.URL.Path != path {
						t.Error("unexpected mutation target", r.Method, r.URL.Path)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
					}
					space, ok := body[field].(map[string]any)
					if !ok || space["gatewayIpAddress"] != desiredGateway {
						t.Error("missing desired gateway")
					}
					dns, ok := space["dnsServers"].([]any)
					if !ok || len(dns) != 1 || dns[0] != "192.0.2.53" {
						t.Error("unowned DNS was not preserved")
					}
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				}
				var rows any
				switch r.URL.Path {
				case globalPoolsPath:
					rows = pools
				case sitePoolsPath:
					rows = reservations
				case sitesPath:
					rows = sites
				default:
					t.Error("unexpected read", r.URL.Path)
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			e := &ipamConfigurationExecutor{client: c, intent: intent}
			write, err := e.next(ctx)
			if err != nil || write == nil {
				t.Fatal("initial plan", err)
			}
			switch mode {
			case "pool-replaced":
				pools[0].ID = "replacement"
			case "pool-preserved-field":
				dns := []string{"192.0.2.54"}
				pools[0].Space.DNS = &dns
			case "pool-converged":
				pools[0].Space.Gateway = &desiredGateway
			case "pool-created-late":
				pools = fixturePools()
			case "reservation-replaced":
				reservations[0].ID = "replacement"
			case "parent-replaced":
				pools[0].ID = "replacement"
				reservations[0].V4.GlobalPoolID = "replacement"
			case "site-replaced":
				for i := range sites {
					if sites[i].ID == "area" {
						sites[i].ID = "replacement"
					}
				}
			case "reservation-preserved-field":
				dns := []string{"192.0.2.54"}
				reservations[0].V4.DNS = &dns
			case "reservation-converged":
				reservations[0].V4.Gateway = &desiredGateway
			}
			_, err = write.submit(ctx)
			stable := mode == "pool-stable" || mode == "reservation-stable"
			wantWrites := 0
			if stable {
				wantWrites = 1
			}
			if (err == nil) != stable || writes != wantWrites {
				t.Fatal("stale plan reached mutation", err, writes)
			}
		})
	}
}
