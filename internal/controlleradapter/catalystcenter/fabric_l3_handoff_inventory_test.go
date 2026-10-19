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

func TestFabricL3IPHandoffInventory(t *testing.T) {
	for _, mode := range []string{"valid", "foreign-device", "foreign-fabric", "missing-transit", "reserved-vlan", "bad-address", "bad-mss", "duplicate-id", "duplicate-mapping", "duplicate-tag", "failed-page", "missing-response", "unexpected-status"} {
		t.Run(mode, func(t *testing.T) {
			pages := 0
			row := func(i int) fabricL3IPHandoff {
				return fabricL3IPHandoff{ID: fmt.Sprintf("handoff-%d", i), DeviceID: "device", FabricID: "fabric", TransitID: "transit", Port: "GigabitEthernet1/0/1", Network: fmt.Sprintf("VN%d", i), VLAN: 2100 + i, Local4: "192.0.2.1/30", Peer4: "192.0.2.2/30"}
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				pages++
				if r.Method != "GET" || r.URL.Path != fabricL3IPHandoffsPath || r.URL.Query().Get("fabricId") != "fabric" || r.URL.Query().Get("networkDeviceId") != "device" || r.URL.Query().Get("limit") != "500" {
					t.Error("unscoped inventory request")
				}
				rows := []fabricL3IPHandoff{}
				if pages == 1 {
					if r.URL.Query().Get("offset") != "1" {
						t.Error("incorrect first offset")
					}
					for i := 0; i < 500; i++ {
						rows = append(rows, row(i))
					}
				} else {
					if pages != 2 || r.URL.Query().Get("offset") != "501" {
						t.Error("incorrect continuation")
					}
					last := row(500)
					switch mode {
					case "foreign-device":
						last.DeviceID = "other"
					case "foreign-fabric":
						last.FabricID = "other"
					case "missing-transit":
						last.TransitID = ""
					case "reserved-vlan":
						last.VLAN = 2046
					case "bad-address":
						last.Local4 = "2001:db8::1/64"
					case "bad-mss":
						bad := 1441
						last.MSS = &bad
					case "duplicate-id":
						last.ID = row(0).ID
					case "duplicate-mapping":
						last.Network = row(0).Network
					case "duplicate-tag":
						last.VLAN = row(0).VLAN
					case "failed-page":
						w.WriteHeader(400)
						return
					case "missing-response":
						fmt.Fprint(w, `{}`)
						return
					case "unexpected-status":
						w.WriteHeader(202)
					}
					rows = append(rows, last)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			got, err := c.fabricL3IPHandoffs(context.Background(), "fabric", "device")
			if mode == "valid" {
				if err != nil || len(got) != 501 || got[500].Local4 != "192.0.2.1/30" {
					t.Fatal("incomplete inventory", len(got), err)
				}
			} else if err == nil || got != nil {
				t.Fatal("invalid partial inventory returned")
			}
			if pages != 2 {
				t.Fatal("pagination did not reach the second page")
			}
		})
	}
}
