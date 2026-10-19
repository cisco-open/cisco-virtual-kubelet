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

func TestFabricPortAssignmentGroups(t *testing.T) {
	for _, mode := range []string{"valid", "l2", "omitted", "missing-data", "missing-binding", "no-network", "conflict", "missing-group", "wrong-case", "repeated-vn", "duplicate-binding"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if r.Method != "GET" || r.URL.Path != fabricVirtualNetworkGroupsPath || r.URL.Query().Get("virtualNetworkName") != "Users" {
					t.Error("unexpected request", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				groups := []string{"Employees"}
				if mode == "missing-group" {
					groups = []string{}
				}
				if mode == "wrong-case" {
					groups = []string{"employees"}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"virtualNetworkName": "Users", "virtualNetworkContextId": "context", "isGuestVirtualNetwork": false, "scalableGroupNames": groups, "status": "success"}); err != nil {
					t.Error(err)
				}
			})
			port, data, group := "GigabitEthernet1/0/1", "DATA", "Employees"
			want := fabricPortAssignmentIntent{Interface: &port, Type: "USER_DEVICE", DataVLAN: &data, SecurityGroup: &group}
			bindings := []fabricPortAssignmentVLANBinding{{Name: "DATA", Traffic: "DATA", VLAN: 100, Gateway: json.RawMessage(`{"virtualNetworkName":"Users"}`)}}
			switch mode {
			case "l2":
				bindings[0].Gateway = nil
				bindings[0].L2 = &fabricL2Network{L3: "Users"}
			case "omitted":
				want.SecurityGroup = nil
			case "missing-data":
				want.DataVLAN = nil
			case "missing-binding":
				bindings = nil
			case "no-network":
				bindings[0].Gateway = nil
				bindings[0].L2 = &fabricL2Network{}
			case "conflict":
				bindings[0].L2 = &fabricL2Network{L3: "Other"}
			case "duplicate-binding":
				bindings = append(bindings, bindings[0])
			}
			wants := []fabricPortAssignmentIntent{want}
			if mode == "repeated-vn" {
				copy := want
				second := "GigabitEthernet1/0/2"
				copy.Interface = &second
				wants = append(wants, copy)
			}
			got, err := c.fabricPortAssignmentGroups(context.Background(), wants, bindings)
			valid := mode == "valid" || mode == "l2" || mode == "omitted" || mode == "repeated-vn"
			if (err == nil) != valid {
				t.Fatal("group resolution outcome", err)
			}
			expectedReads := 0
			if mode == "valid" || mode == "l2" || mode == "repeated-vn" || mode == "missing-group" || mode == "wrong-case" {
				expectedReads = 1
			}
			if reads != expectedReads {
				t.Fatal("read count", reads)
			}
			if !valid {
				if got != nil {
					t.Fatal("partial bindings returned")
				}
				return
			}
			if mode == "omitted" {
				if len(got) != 0 {
					t.Fatal("invented dependency")
				}
				return
			}
			if len(got) != 1 || got[0].ContextID != "context" || got[0].Network != "Users" {
				t.Fatal("incomplete native membership")
			}
		})
	}
}
