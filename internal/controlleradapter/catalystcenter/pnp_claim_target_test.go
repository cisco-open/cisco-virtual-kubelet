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

func TestPnPClaimTargetFootprint(t *testing.T) {
	for _, mode := range []string{"single", "stack", "missing-member", "extra-member", "split-records", "foreign-id", "wrong-primary", "wrong-pid", "wrong-name", "managed-member", "native-alias"} {
		t.Run(mode, func(t *testing.T) {
			wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			if mode != "single" {
				want.Serial += ",FOC87654321"
			}
			row := pnpFixture("owned-id", "FOC12345678")
			info := row["deviceInfo"].(map[string]any)
			info["hostname"] = "edge"
			rows := []any{row}
			if mode != "single" {
				info["stack"] = true
				member := "FOC87654321"
				if mode == "extra-member" {
					member = "FOC99999999"
				}
				info["stackInfo"] = map[string]any{"stackMemberList": []any{map[string]any{"serialNumber": member, "pid": "C9300-48P"}}}
			}
			switch mode {
			case "missing-member":
				delete(info, "stackInfo")
			case "split-records":
				delete(info, "stackInfo")
				rows = append(rows, pnpFixture("other-id", "FOC87654321"))
			case "foreign-id":
				row["id"] = "foreign-id"
			case "wrong-primary":
				info["serialNumber"] = "FOC87654321"
				info["stackInfo"] = map[string]any{"stackMemberList": []any{map[string]any{"serialNumber": "FOC12345678", "pid": "C9300-48P"}}}
			case "wrong-pid":
				info["pid"] = "OTHER-PID"
			case "wrong-name":
				info["hostname"] = "other"
			case "native-alias":
				rows = append(rows, pnpFixture("alias-id", "FOC87654321"))
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case pnpDevicesPath:
					json.NewEncoder(w).Encode(rows)
				case devicesPath:
					inventory := []Device{}
					if mode == "managed-member" {
						inventory = append(inventory, Device{ID: "managed", Serial: "FOC99999999,foc87654321"})
					}
					json.NewEncoder(w).Encode(map[string]any{"response": inventory})
				default:
					t.Error("unexpected API")
					w.WriteHeader(404)
				}
			})
			got, err := c.pnpClaimTarget(context.Background(), want, "owned-id")
			valid := mode == "single" || mode == "stack"
			if (err == nil) != valid {
				t.Fatal("footprint result", err)
			}
			if valid && (got.device.ID != "owned-id" || len(got.serials) != 1 && mode == "single" || len(got.serials) != 2 && mode == "stack") {
				t.Fatal("incomplete physical binding")
			}
		})
	}
}
