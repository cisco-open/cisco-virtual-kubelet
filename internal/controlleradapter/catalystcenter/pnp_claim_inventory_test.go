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

func TestPnPClaimInventoryObservation(t *testing.T) {
	for _, mode := range []string{"stable", "missing-inventory", "unassigned", "wrong-pnp-id", "wrong-pnp-site", "wrong-site-id", "network-alias", "late-network-id", "late-assignment", "late-state", "late-software"} {
		t.Run(mode, func(t *testing.T) {
			wants, err := parsePnPInventory([]byte(pnpIntentFixture()))
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			late := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("observation mutated controller")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case pnpDevicesPath:
					row := pnpFixture("pnp-id", "FOC12345678")
					info := row["deviceInfo"].(map[string]any)
					info["hostname"] = "edge"
					info["siteId"] = "building"
					if mode == "wrong-pnp-id" {
						row["id"] = "other-pnp"
					}
					if mode == "wrong-pnp-site" {
						info["siteId"] = "other-site"
					}
					if late && mode == "late-state" {
						info["state"] = "changed"
					}
					json.NewEncoder(w).Encode([]any{row})
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case devicesPath:
					device := Device{ID: "network-id", Serial: "foc12345678", Hostname: "edge", SoftwareVersion: "17.18.04", Reachability: "Reachable"}
					if late && mode == "late-network-id" {
						device.ID = "replacement-id"
					}
					if late && mode == "late-software" {
						device.SoftwareVersion = "26.02.01"
					}
					rows := []Device{device}
					if mode == "missing-inventory" {
						rows = []Device{}
					}
					if mode == "network-alias" {
						device.ID = "alias-id"
						rows = append(rows, device)
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case assignedDevicesPath:
					if r.URL.Query().Get("siteId") != "building" {
						t.Error("assignment query lost site binding")
					}
					rows := []map[string]string{{"deviceId": "network-id", "siteId": "building"}}
					if mode == "unassigned" || late && mode == "late-assignment" {
						rows = []map[string]string{}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": rows})
				default:
					t.Error("unexpected API", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			siteID := "building"
			if mode == "wrong-site-id" {
				siteID = "replacement-site"
			}
			got, err := c.pnpClaimInventorySnapshot(context.Background(), want, "pnp-id", siteID)
			valid := !strings.HasPrefix(mode, "wrong-") && mode != "network-alias"
			if (err == nil) != valid {
				t.Fatal("observation result", err)
			}
			if !valid {
				return
			}
			if (got.inventory == nil) != (mode == "missing-inventory") || got.assigned != (mode != "missing-inventory" && mode != "unassigned") {
				t.Fatal("pending inventory/assignment misreported")
			}
			if got.inventory != nil && got.inventory.ID != "network-id" {
				t.Fatal("PnP UUID substituted for network UUID")
			}
			late = true
			err = c.revalidatePnPClaimInventory(context.Background(), want, "pnp-id", siteID, got)
			if (err != nil) != strings.HasPrefix(mode, "late-") {
				t.Fatal("late observation change", err)
			}
		})
	}
}

func TestPnPClaimNetworkStackIdentity(t *testing.T) {
	serials := []string{"FOC12345678", "FOC87654321"}
	for _, mode := range []string{"exact", "reverse", "partial", "extra", "duplicate-member", "duplicate-record", "split-records", "malformed-member", "empty"} {
		t.Run(mode, func(t *testing.T) {
			rows := []Device{{ID: "network-id", Serial: "FOC12345678,FOC87654321"}}
			switch mode {
			case "reverse":
				rows[0].Serial = "foc87654321,foc12345678"
			case "partial":
				rows[0].Serial = "FOC12345678"
			case "extra":
				rows[0].Serial += ",FOC99999999"
			case "duplicate-member":
				rows[0].Serial += ",foc12345678"
			case "duplicate-record":
				rows = append(rows, Device{ID: "alias", Serial: rows[0].Serial})
			case "split-records":
				rows[0].Serial = "FOC12345678"
				rows = append(rows, Device{ID: "other", Serial: "FOC87654321"})
			case "malformed-member":
				rows[0].Serial += ", invalid"
			case "empty":
				rows = nil
			}
			got, err := bindPnPClaimNetworkDevice(serials, rows)
			valid := mode == "exact" || mode == "reverse" || mode == "empty"
			if (err == nil) != valid {
				t.Fatal("network stack binding", err)
			}
			if mode == "empty" {
				if got != nil {
					t.Fatal("invented inventory device")
				}
				return
			}
			if valid && (got == nil || got.Serial != "foc12345678,foc87654321") {
				t.Fatal("lost canonical complete stack")
			}
		})
	}
}
