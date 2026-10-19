// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

func pnpFixture(id, serial string) map[string]any {
	return map[string]any{"id": id, "deviceInfo": map[string]any{"serialNumber": serial, "pid": "C9300-48P", "state": "Unclaimed", "aaaCredentials": map[string]any{"password": "private-value"}, "httpHeaders": []any{map[string]any{"value": "private-value"}}}, "dayZeroConfig": map[string]any{"config": "private-value"}, "workflow": map[string]any{"_id": "workflow", "state": "native-state", "tasks": []any{map[string]any{"outputStr": "private-value"}}}}
}

func TestPnPDeviceInventory(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "paged", "repeat", "oversized", "missing-id", "missing-info", "duplicate-serial", "stack", "stack-alias", "bad-member", "conflicting-site", "null", "envelope", "partial", "no-content"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" || r.URL.Path != pnpDevicesPath || r.URL.Query().Get("limit") != "100" {
					t.Error("wrong request", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				offset, err := strconv.Atoi(r.URL.Query().Get("offset"))
				if err != nil || offset < 0 || calls == 1 && offset != 0 {
					t.Error("wrong zero-based offset")
					w.WriteHeader(400)
					return
				}
				row := pnpFixture("device", "FOC12345678")
				if mode == "valid" {
					row["runSummaryList"] = []any{map[string]any{"details": "private-value", "errorFlag": false, "timestamp": 123.0, "historyTaskInfo": map[string]any{"command": "private-value", "outputStr": "private-value"}}}
				}

				info := row["deviceInfo"].(map[string]any)
				rows := []map[string]any{row}
				switch mode {
				case "empty":
					rows = []map[string]any{}
				case "missing-id":
					delete(row, "id")
				case "missing-info":
					delete(row, "deviceInfo")
				case "duplicate-serial":
					rows = append(rows, pnpFixture("other", "foc12345678"))
				case "conflicting-site":
					info["siteId"] = "site-a"
					info["location"] = map[string]any{"siteId": "site-b"}
				case "stack", "stack-alias", "bad-member":
					info["stack"] = true
					serial := "FOC87654321"
					if mode == "bad-member" {
						serial = "a,b"
					}
					info["stackInfo"] = map[string]any{"stackMemberList": []any{map[string]any{"serialNumber": "FOC12345678", "pid": "C9300-48P"}, map[string]any{"serialNumber": serial, "pid": "C9300-48P"}}}
					if mode == "stack-alias" {
						rows = append(rows, pnpFixture("other", "foc87654321"))
					}
				case "null":
					fmt.Fprint(w, `null`)
					return
				case "envelope":
					fmt.Fprint(w, `{"response":[]}`)
					return
				case "partial":
					w.WriteHeader(206)
				case "no-content":
					w.WriteHeader(204)
					return
				case "paged", "repeat", "oversized":
					if mode == "repeat" {
						offset = 0
					}
					end := offset + 100
					if end > 101 {
						end = 101
					}
					if mode == "oversized" {
						end = offset + 101
					}
					rows = []map[string]any{}
					for i := offset; i < end; i++ {
						rows = append(rows, pnpFixture(fmt.Sprintf("id-%03d", i), fmt.Sprintf("SERIAL-%03d", i)))
					}
				}
				json.NewEncoder(w).Encode(rows)
			})
			rows, err := c.pnpDevices(context.Background())
			valid := mode == "valid" || mode == "empty" || mode == "paged" || mode == "stack"
			if (err == nil) != valid {
				t.Fatal("unexpected inventory result", err)
			}
			if err == nil {
				raw, err := json.Marshal(rows)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(raw), "private-value") || strings.Contains(string(raw), "aaaCredentials") || strings.Contains(string(raw), "dayZeroConfig") {
					t.Fatal("sensitive native fields retained")
				}
			}
			if mode == "paged" && (len(rows) != 101 || calls != 2) {
				t.Fatal("incomplete pagination", len(rows), calls)
			}
			if mode == "valid" && (rows[0].Info.Serial != "foc12345678" || rows[0].Workflow.ID != "workflow") {
				t.Fatal("lost native identity")
			}
			if mode == "stack" && len(rows[0].Info.StackInfo.Members) != 2 {
				t.Fatal("lost stack footprint")
			}
		})
	}
}
