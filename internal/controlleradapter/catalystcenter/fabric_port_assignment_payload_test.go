// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestFabricPortAssignmentPayload(t *testing.T) {
	for _, mode := range []string{"valid", "options", "range", "missing", "mapped", "foreign", "duplicate-id", "invalid-fabric", "empty"} {
		t.Run(mode, func(t *testing.T) {
			_, wants, err := parseFabricPortAssignmentDeployment(portAssignmentTestModel("[" + portAssignmentTestRow + "]"))
			if err != nil {
				t.Fatal(err)
			}
			rows := wants[0].assignments
			ports := []lanLinkInterface{{ID: "port-1", DeviceID: "edge-id", PortName: "GigabitEthernet1/0/1", Type: "Physical"}, {ID: "port-2", DeviceID: "edge-id", PortName: "GigabitEthernet1/0/2", Type: "Physical"}}
			fabric := "fabric"
			if mode == "options" {
				description, vlan, voice, group, auth := "", "DATA", "VOICE", "Employees", "Closed Authentication"
				rows[0].Description = &description
				rows[0].DataVLAN = &vlan
				rows[0].VoiceVLAN = &voice
				rows[0].SecurityGroup = &group
				rows[0].Authentication = &auth
			}
			if mode == "range" || mode == "duplicate-id" {
				value := "GigabitEthernet1/0/1-1/0/2"
				rows[0].Interface = nil
				rows[0].Range = &value
			}
			switch mode {
			case "missing":
				ports = ports[1:]
			case "mapped":
				ports[0].MappedID = "mapped"
			case "foreign":
				ports[0].DeviceID = "another-device"
			case "duplicate-id":
				ports[1].ID = ports[0].ID
			case "invalid-fabric":
				fabric = ""
			case "empty":
				rows = nil
			}
			raw, err := fabricPortAssignmentPayload("edge-id", fabric, rows, ports)
			valid := mode == "valid" || mode == "options" || mode == "range"
			if (err == nil) != valid {
				t.Fatal("payload acceptance", err)
			}
			if !valid {
				return
			}
			var payload []map[string]any
			if json.Unmarshal(raw, &payload) != nil {
				t.Fatal("invalid JSON")
			}
			count := 1
			if mode == "range" {
				count = 2
			}
			if len(payload) != count {
				t.Fatal("payload count")
			}
			for i, row := range payload {
				if row["networkDeviceId"] != "edge-id" || row["fabricId"] != "fabric" || row["interfaceName"] != ports[i].PortName || row["connectedDeviceType"] != "USER_DEVICE" {
					t.Fatal("wrong native mapping", row)
				}
				if mode == "options" {
					if len(row) != 9 || row["interfaceDescription"] != "" || row["dataVlanName"] != "DATA" || row["voiceVlanName"] != "VOICE" || row["securityGroupName"] != "Employees" || row["authenticateTemplateName"] != "Closed Authentication" {
						t.Fatal("optional intent lost", row)
					}
				} else if len(row) != 4 {
					t.Fatal("invented defaults or model fields", row)
				}
			}
		})
	}
}
