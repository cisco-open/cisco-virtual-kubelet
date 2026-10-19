// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func lanSessionFixture() lanSession {
	return lanSession{ID: "session", Site: "Global/Lab", PrimaryIP: "192.0.2.10", PeerIP: "192.0.2.11", Ports: []string{"TenGigabitEthernet1/0/1"}, Pools: []lanSessionPool{{Name: "underlay", Role: "MAIN_POOL"}}, Action: "STOP", Status: "native descriptive text", Created: "2026-10-13T10:00:00Z", Discovered: []lanDiscoveredDevice{{Name: "edge", Serial: "FOC12345678", State: "Added to inventory", Addresses: []string{"192.0.2.20"}}}}
}

func TestLANSessionsInventory(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "paged", "repeat", "too-many", "unknown-state", "missing-pools", "missing-ports", "foreign-ip", "duplicate-port", "duplicate-pool", "duplicate-discovered", "bad-address", "null", "partial", "no-content"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" || r.URL.Path != lanSessionStatusPath || r.URL.Query().Get("limit") != "10" {
					t.Error("wrong request", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
				if err != nil || offset < 1 {
					t.Error("wrong offset")
					w.WriteHeader(400)
					return
				}
				row := lanSessionFixture()
				rows := []lanSession{row}
				switch mode {
				case "empty":
					rows = []lanSession{}
				case "unknown-state":
					rows[0].Action = "FUTURE_ACTION"
					rows[0].Status = "STOP requested; processing pending"
				case "missing-pools":
					rows[0].Pools = nil
				case "missing-ports":
					rows[0].Ports = nil
				case "foreign-ip":
					rows[0].PrimaryIP = "seed.example"
				case "duplicate-port":
					rows[0].Ports = append(rows[0].Ports, "tengigabitethernet1/0/1")
				case "duplicate-pool":
					rows[0].Pools = append(rows[0].Pools, row.Pools[0])
				case "duplicate-discovered":
					other := row.Discovered[0]
					other.Serial = "foc12345678"
					rows[0].Discovered = append(rows[0].Discovered, other)
				case "bad-address":
					rows[0].Discovered[0].Addresses = []string{"not-an-ip"}
				case "null":
					fmt.Fprint(w, `{"response":null}`)
					return
				case "partial":
					w.WriteHeader(206)
				case "no-content":
					w.WriteHeader(204)
					return
				case "paged", "repeat", "too-many":
					if mode == "repeat" {
						offset = 1
					}
					end := offset + 10
					if end > 12 {
						end = 12
					}
					if mode == "too-many" {
						end = offset + 11
					}
					rows = []lanSession{}
					for i := offset; i < end; i++ {
						r := lanSessionFixture()
						r.ID = fmt.Sprintf("session-%02d", i)
						rows = append(rows, r)
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			rows, err := c.lanSessions(context.Background())
			valid := mode == "valid" || mode == "empty" || mode == "paged" || mode == "unknown-state"
			if (err == nil) != valid {
				t.Fatal(rows, err)
			}
			if mode == "paged" && (calls != 2 || len(rows) != 11) {
				t.Fatal("incomplete paging", calls, len(rows))
			}
			if mode == "valid" && rows[0].Discovered[0].Serial != "foc12345678" {
				t.Fatal("uncanonical serial")
			}
			if mode == "unknown-state" && rows[0].Action != "FUTURE_ACTION" {
				t.Fatal("unknown state was rewritten")
			}
		})
	}
}
