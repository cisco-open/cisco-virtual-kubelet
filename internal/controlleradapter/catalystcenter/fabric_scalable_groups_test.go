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

func TestFabricScalableGroups(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "missing-groups", "null-groups", "duplicate", "wrong-network", "missing-context", "missing-guest", "failed", "wrapped", "unauthorized"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if r.Method != "GET" || r.URL.Path != fabricVirtualNetworkGroupsPath || r.URL.Query().Get("virtualNetworkName") != "Users & Guests" {
					t.Error("unexpected request", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				if mode == "unauthorized" {
					w.WriteHeader(403)
					return
				}
				row := map[string]any{"virtualNetworkName": "Users & Guests", "virtualNetworkContextId": "vn-context", "isGuestVirtualNetwork": false, "scalableGroupNames": []string{"Printers", "Employees"}, "status": "success"}
				switch mode {
				case "empty":
					row["scalableGroupNames"] = []string{}
				case "missing-groups":
					delete(row, "scalableGroupNames")
				case "null-groups":
					row["scalableGroupNames"] = nil
				case "duplicate":
					row["scalableGroupNames"] = []string{"Employees", "employees"}
				case "wrong-network":
					row["virtualNetworkName"] = "Different"
				case "missing-context":
					delete(row, "virtualNetworkContextId")
				case "missing-guest":
					delete(row, "isGuestVirtualNetwork")
				case "failed":
					row["status"] = "failed"
				case "wrapped":
					row = map[string]any{"response": row}
				}
				if err := json.NewEncoder(w).Encode(row); err != nil {
					t.Error(err)
				}
			})
			got, err := c.fabricScalableGroups(context.Background(), "Users & Guests")
			valid := mode == "valid" || mode == "empty"
			if (err == nil) != valid {
				t.Fatal("membership observation outcome", err)
			}
			if reads != 1 {
				t.Fatal("unexpected native reads", reads)
			}
			if !valid {
				if got != nil {
					t.Fatal("partial evidence retained")
				}
				return
			}
			if got.ContextID != "vn-context" || got.Guest || got.Network != "Users & Guests" {
				t.Fatal("incomplete identity")
			}
			if mode == "valid" && (len(got.Groups) != 2 || got.Groups[0] != "Employees" || got.Groups[1] != "Printers") {
				t.Fatal("lost or unstable group list")
			}
			if mode == "empty" && len(got.Groups) != 0 {
				t.Fatal("invented membership")
			}
		})
	}
}
