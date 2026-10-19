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

func TestPnPRegistrationNativeLifecycle(t *testing.T) {
	for _, mode := range []string{"valid", "id-only", "existing", "managed", "managed-stack-member", "late-existing", "lost", "async", "unauthorized", "conflict", "wrong-response", "replaced-readback", "pending-readback"} {
		t.Run(mode, func(t *testing.T) {
			want := pnpDeviceIntent{Name: "edge", State: "PNP", Serial: "FOC12345678", Site: "Global/Lab/Building", PID: "C9300-48P"}
			writes := 0
			planned := false
			readback := false
			row := pnpFixture("created-id", "FOC12345678")
			row["deviceInfo"].(map[string]any)["hostname"] = "edge"
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path == devicesPath && r.Method == "GET" {
					rows := []Device{}
					if mode == "managed" {
						rows = append(rows, Device{ID: "managed", Serial: "foc12345678"})
					}
					if mode == "managed-stack-member" {
						rows = append(rows, Device{ID: "managed", Serial: "OTHER,foc12345678"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
					return
				}
				if r.URL.Path != pnpDevicesPath {
					t.Error("unexpected API", r.URL)
					w.WriteHeader(404)
					return
				}
				if r.Method == "GET" {
					rows := []map[string]any{}
					if mode == "existing" || mode == "late-existing" && planned || writes > 0 && mode != "pending-readback" {
						rows = append(rows, row)
					}
					if readback && mode == "replaced-readback" {
						row["id"] = "foreign-id"
					}
					json.NewEncoder(w).Encode(rows)
					return
				}
				if r.Method != "POST" {
					t.Error("unexpected mutation", r.Method)
					w.WriteHeader(400)
					return
				}
				writes++
				var body map[string]map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || body["deviceInfo"]["serialNumber"] != "FOC12345678" {
					t.Error("bad registration payload")
				}
				switch mode {
				case "lost":
					fmt.Fprint(w, `{`)
					return
				case "async":
					w.WriteHeader(202)
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					return
				case "unauthorized":
					w.WriteHeader(401)
					return
				case "conflict":
					w.WriteHeader(409)
					fmt.Fprint(w, `{"response":{"errorCode":"NCOB01019"}}`)
					return
				case "id-only":
					fmt.Fprint(w, `{"id":"created-id"}`)
					return
				case "wrong-response":
					row["deviceInfo"].(map[string]any)["serialNumber"] = "OTHER"
				}
				json.NewEncoder(w).Encode(row)
			})
			plan, err := c.planPnPRegistration(context.Background(), want)
			if mode == "existing" || mode == "managed" || mode == "managed-stack-member" {
				if err == nil || writes != 0 {
					t.Fatal("adopted existing hardware")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			planned = true
			receipt, err := plan.submit(context.Background())
			accepted := mode == "valid" || mode == "id-only" || mode == "replaced-readback" || mode == "pending-readback"
			if (err == nil) != accepted {
				t.Fatal("wrong submission result", err)
			}
			if _, err := plan.submit(context.Background()); err == nil {
				t.Fatal("replayed plan")
			}
			expectedWrites := 1
			if mode == "late-existing" {
				expectedWrites = 0
			}
			if writes != expectedWrites {
				t.Fatal("wrong native write count", writes)
			}
			if accepted {
				readback = true
				done, err := c.observePnPRegistration(context.Background(), receipt)
				if mode == "replaced-readback" {
					if err == nil {
						t.Fatal("adopted replacement UUID")
					}
					return
				}
				if err != nil || done != (mode != "pending-readback") {
					t.Fatal("wrong readback", done, err)
				}
			}
		})
	}
}
