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

func TestFabricSDAHandoffInventory(t *testing.T) {
	for _, mode := range []string{"valid", "scoped", "empty", "foreign-fabric", "foreign-device", "missing-transit", "negative-prime", "overflow-decider", "duplicate-device", "failed-page", "missing-response", "unexpected-status", "bad-type"} {
		t.Run(mode, func(t *testing.T) {
			pages := 0
			scoped := mode == "scoped" || mode == "foreign-device"
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				pages++
				q := r.URL.Query()
				if r.Method != "GET" || r.URL.Path != fabricSDAHandoffsPath || q.Get("fabricId") != "fabric" || q.Get("limit") != "500" || (q.Get("networkDeviceId") == "device-0") != scoped {
					t.Error("incorrect scoped read")
				}
				rows := []fabricSDAHandoff{}
				row := func(i int) fabricSDAHandoff {
					return fabricSDAHandoff{DeviceID: fmt.Sprintf("device-%d", i), FabricID: "fabric", TransitID: "transit"}
				}
				if mode == "empty" {
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				}
				if pages == 1 && !scoped {
					if q.Get("offset") != "1" {
						t.Error("wrong first offset")
					}
					for i := 0; i < 500; i++ {
						rows = append(rows, row(i))
					}
				} else {
					if !scoped && (pages != 2 || q.Get("offset") != "501") {
						t.Error("wrong continuation")
					}
					last := row(500)
					if scoped {
						last = row(0)
					}
					switch mode {
					case "foreign-fabric":
						last.FabricID = "other"
					case "foreign-device":
						last.DeviceID = "other"
					case "missing-transit":
						last.TransitID = ""
					case "negative-prime":
						v := int64(-1)
						last.Prime = &v
					case "overflow-decider":
						v := int64(2147483648)
						last.Decider = &v
					case "duplicate-device":
						last.DeviceID = "device-0"
						last.TransitID = "other-transit"
					case "failed-page":
						w.WriteHeader(400)
						return
					case "missing-response":
						fmt.Fprint(w, `{}`)
						return
					case "unexpected-status":
						w.WriteHeader(202)
					case "bad-type":
						fmt.Fprint(w, `{"response":[{"networkDeviceId":"device-500","fabricId":"fabric","transitNetworkId":"transit","connectedToInternet":"false"}]}`)
						return
					}
					rows = append(rows, last)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			device := ""
			if scoped {
				device = "device-0"
			}
			rows, err := c.fabricSDAHandoffs(context.Background(), "fabric", device)
			valid := mode == "valid" || mode == "scoped" || mode == "empty"
			if (err == nil) != valid {
				t.Fatal("inventory outcome", err)
			}
			if !valid {
				if rows != nil {
					t.Fatal("partial inventory escaped")
				}
				return
			}
			count := 501
			if scoped {
				count = 1
			}
			if mode == "empty" {
				count = 0
			}
			if len(rows) != count {
				t.Fatal("incomplete inventory", len(rows))
			}
			before := pages
			if _, err := c.fabricSDAHandoffs(context.Background(), "", device); err == nil || pages != before {
				t.Fatal("invalid scope reached API")
			}
		})
	}
}
