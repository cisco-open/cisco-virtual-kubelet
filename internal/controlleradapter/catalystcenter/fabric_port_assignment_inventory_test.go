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

func TestFabricPortAssignmentInventory(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "paged", "duplicate-id", "duplicate-port", "foreign-device", "foreign-fabric", "missing-port", "missing-type", "invalid-auth", "partial", "repeat-page"} {
		t.Run(mode, func(t *testing.T) {
			pages := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				pages++
				if r.Method != "GET" || r.URL.Path != fabricPortAssignmentsPath || r.URL.Query().Get("fabricId") != "fabric" || r.URL.Query().Get("networkDeviceId") != "device" || r.URL.Query().Get("limit") != "50" {
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
						end = 50
					} else {
						if offset != 51 {
							t.Error("unexpected offset", offset)
						}
						start, end = 51, 51
					}
					if mode == "repeat-page" {
						start, end = 1, 50
					}
				}
				rows := []fabricPortAssignment{}
				for i := start; i <= end; i++ {
					rows = append(rows, fabricPortAssignment{ID: fmt.Sprintf("assignment-%04d", i), FabricID: "fabric", DeviceID: "device", Interface: fmt.Sprintf("GigabitEthernet1/0/%d", i), Type: "USER_DEVICE"})
				}
				if len(rows) > 0 {
					switch mode {
					case "duplicate-id", "duplicate-port":
						copy := rows[0]
						copy.ID = "different"
						copy.Interface = "GigabitEthernet1/0/2"
						if mode == "duplicate-id" {
							copy.ID = rows[0].ID
						}
						if mode == "duplicate-port" {
							copy.Interface = "gigabitethernet1/0/1"
						}
						rows = append(rows, copy)
					case "foreign-device":
						rows[0].DeviceID = "other"
					case "foreign-fabric":
						rows[0].FabricID = "other"
					case "missing-port":
						rows[0].Interface = ""
					case "missing-type":
						rows[0].Type = ""
					case "invalid-auth":
						value := "INVALID"
						rows[0].Authentication = &value
					}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": rows}); err != nil {
					t.Error(err)
				}
			})
			rows, err := c.fabricPortAssignments(context.Background(), "fabric", "device")
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
				expected = 51
			}
			if len(rows) != expected || mode == "paged" && pages != 2 {
				t.Fatal("incomplete inventory", len(rows), pages)
			}
		})
	}
}
