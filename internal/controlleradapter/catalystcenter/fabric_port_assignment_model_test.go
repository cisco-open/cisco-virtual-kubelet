// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

const portAssignmentTestRow = `{"interface_name":"GigabitEthernet1/0/1","connected_device_type":"USER_DEVICE"}`

func portAssignmentTestModel(rows string) []byte {
	return []byte(strings.Replace(fabricEdgeIntentModel, `"name":"edge"`, `"name":"edge","port_assignments":`+rows, 1))
}

func TestFabricPortAssignmentModel(t *testing.T) {
	for _, mode := range []string{"single", "range", "options", "duplicate", "range-overlap", "both", "neither", "unknown", "empty", "null", "invalid-type", "invalid-auth", "blank-reference", "control-description", "cross-module", "reverse", "oversized", "overflow", "empty-interface", "wrong-role", "unprovisioned", "other-effect"} {
		t.Run(mode, func(t *testing.T) {
			var row map[string]any
			if err := json.Unmarshal([]byte(portAssignmentTestRow), &row); err != nil {
				t.Fatal(err)
			}
			rows := []any{row}
			switch mode {
			case "range", "range-overlap", "cross-module", "reverse", "oversized", "overflow":
				delete(row, "interface_name")
				row["interfaces_range"] = "GigabitEthernet1/0/1-1/0/3"
				if mode == "cross-module" {
					row["interfaces_range"] = "GigabitEthernet1/0/1-2/0/3"
				}
				if mode == "reverse" {
					row["interfaces_range"] = "GigabitEthernet1/0/3-1/0/1"
				}
				if mode == "oversized" {
					row["interfaces_range"] = "GigabitEthernet1/0/1-1/0/1001"
				}
				if mode == "overflow" {
					row["interfaces_range"] = "GigabitEthernet1/0/1-1/0/99999999999999999999"
				}
				if mode == "range-overlap" {
					rows = append(rows, map[string]any{"interface_name": "gigabitethernet1/0/2", "connected_device_type": "ACCESS_POINT"})
				}
			case "options":
				row["data_vlan_name"] = "DATA"
				row["voice_vlan_name"] = "VOICE"
				row["security_group_name"] = "Employees"
				row["authenticate_template_name"] = "Closed Authentication"
				row["interface_description"] = ""
			case "duplicate":
				rows = append(rows, row)
			case "both":
				row["interfaces_range"] = "GigabitEthernet1/0/1-1/0/3"
			case "neither":
				delete(row, "interface_name")
			case "unknown":
				row["native_id"] = "port-id"
			case "empty":
				rows = []any{}
			case "null":
				rows = nil
			case "invalid-type":
				row["connected_device_type"] = "TRUNK"
			case "invalid-auth":
				row["authenticate_template_name"] = "closed"
			case "blank-reference":
				row["data_vlan_name"] = ""
			case "control-description":
				row["interface_description"] = "line\nline"
			case "empty-interface":
				row["interface_name"] = ""
			}
			encoded, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			raw := portAssignmentTestModel(string(encoded))
			if mode == "wrong-role" {
				raw = []byte(strings.Replace(string(raw), "EDGE_NODE", "CONTROL_PLANE_NODE", 1))
			}
			if mode == "unprovisioned" {
				raw = []byte(strings.Replace(string(raw), "PROVISION", "ASSIGN", 1))
			}
			if mode == "other-effect" {
				raw = []byte(strings.Replace(string(raw), `"port_assignments":`, `"port_channels":[],"port_assignments":`, 1))
			}
			base, wants, err := parseFabricPortAssignmentDeployment(raw)
			valid := mode == "single" || mode == "range" || mode == "options"
			if (err == nil) != valid {
				t.Fatal("model acceptance", err)
			}
			if !valid {
				return
			}
			if len(base) != 1 || len(wants) != 1 {
				t.Fatal("lost base device or assignment")
			}
			count := 1
			if mode == "range" {
				count = 3
			}
			if len(wants[0].assignments) != count {
				t.Fatal("range expansion count")
			}
			for i, got := range wants[0].assignments {
				if got.Range != nil || got.Interface == nil {
					t.Fatal("unresolved port")
				}
				if mode == "range" && *got.Interface != []string{"GigabitEthernet1/0/1", "GigabitEthernet1/0/2", "GigabitEthernet1/0/3"}[i] {
					t.Fatal("incorrect range", *got.Interface)
				}
				if mode != "options" && (got.Authentication != nil || got.DataVLAN != nil || got.Description != nil) {
					t.Fatal("invented defaults")
				}
				if mode == "options" && (got.Description == nil || *got.Description != "" || got.SecurityGroup == nil || *got.SecurityGroup != "Employees") {
					t.Fatal("optional intent lost")
				}
			}
		})
	}
}
