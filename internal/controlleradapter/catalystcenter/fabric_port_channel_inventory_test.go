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

func TestFabricPortChannelInventory(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "paged", "duplicate-id", "duplicate-name", "shared-port", "foreign-device", "foreign-fabric", "missing-name", "missing-protocol", "invalid-protocol", "missing-ports", "partial", "repeat-page"} {
		t.Run(mode, func(t *testing.T) {
			pages := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				pages++
				if r.Method != "GET" || r.URL.Path != fabricPortChannelsPath || r.URL.Query().Get("fabricId") != "fabric" || r.URL.Query().Get("networkDeviceId") != "device" || r.URL.Query().Get("limit") != "500" {
					t.Error("unexpected request", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
				if err != nil {
					t.Error(err)
				}
				if mode == "partial" {
					fmt.Fprint(w, `{"response":{}}`)
					return
				}
				start, end := 1, 1
				if mode == "empty" {
					end = 0
				}
				if mode == "paged" || mode == "repeat-page" {
					if offset == 1 {
						end = 500
					} else {
						if offset != 501 {
							t.Error("unexpected offset", offset)
						}
						start, end = 501, 501
					}
					if mode == "repeat-page" {
						start, end = 1, 500
					}
				}
				rows := []fabricPortChannel{}
				for i := start; i <= end; i++ {
					rows = append(rows, fabricPortChannel{ID: fmt.Sprintf("channel-%04d", i), FabricID: "fabric", DeviceID: "device", Name: fmt.Sprintf("Port-channel%d", i), Interfaces: []string{fmt.Sprintf("GigabitEthernet1/0/%d", i)}, Type: "TRUNK", Protocol: "LACP"})
				}
				if len(rows) > 0 {
					switch mode {
					case "duplicate-id", "duplicate-name", "shared-port":
						copy := rows[0]
						copy.ID = "different"
						copy.Name = "Port-channel2"
						copy.Interfaces = []string{"GigabitEthernet1/0/2"}
						if mode == "duplicate-id" {
							copy.ID = rows[0].ID
						}
						if mode == "duplicate-name" {
							copy.Name = rows[0].Name
						}
						if mode == "shared-port" {
							copy.Interfaces = rows[0].Interfaces
						}
						rows = append(rows, copy)
					case "foreign-device":
						rows[0].DeviceID = "other"
					case "foreign-fabric":
						rows[0].FabricID = "other"
					case "missing-name":
						rows[0].Name = ""
					case "missing-protocol":
						rows[0].Protocol = ""
					case "invalid-protocol":
						rows[0].Protocol = "AUTO"
					case "missing-ports":
						rows[0].Interfaces = nil
					}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
					t.Error(err)
				}
			})
			rows, err := c.fabricPortChannels(context.Background(), "fabric", "device")
			good := mode == "valid" || mode == "empty" || mode == "paged"
			if !good {
				if err == nil || rows != nil {
					t.Fatal("partial/ambiguous inventory accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if mode == "empty" {
				expected = 0
			}
			if mode == "paged" {
				expected = 501
			}
			if len(rows) != expected || mode == "paged" && pages != 2 {
				t.Fatal("incomplete inventory", len(rows), pages)
			}
		})
	}
}
