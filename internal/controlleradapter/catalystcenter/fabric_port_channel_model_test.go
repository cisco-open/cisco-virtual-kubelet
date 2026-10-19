// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"fmt"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"os"
	"sigs.k8s.io/yaml"
	"strings"
	"testing"
)

const portChannelTestRow = `{"id":7,"connected_device_type":"TRUNK","interface_names":["GigabitEthernet1/0/2","GigabitEthernet1/0/1"]}`

func portChannelTestModel(rows string) []byte {
	return []byte(strings.Replace(fabricEdgeIntentModel, `"name":"edge"`, `"name":"edge","port_channels":`+rows, 1))
}
func TestFabricPortChannelModel(t *testing.T) {
	for _, mode := range []string{"valid", "options", "extended", "zone", "duplicate-id", "shared-port", "missing-id", "fractional-id", "wrong-type", "empty", "null", "unknown", "wrong-role", "unprovisioned", "other-effect", "too-many-ports", "pagp-limit", "wrong-protocol", "extended-trunk-fields", "bad-vlan", "duplicate-port"} {
		t.Run(mode, func(t *testing.T) {
			var row map[string]any
			if err := json.Unmarshal([]byte(portChannelTestRow), &row); err != nil {
				t.Fatal(err)
			}
			rows := []any{row}
			switch mode {
			case "options":
				row["protocol"] = "LACP"
				row["description"] = ""
				row["native_vlan_id"] = 100
				row["allowed_vlan_ranges"] = "100,200-210"
			case "extended":
				row["connected_device_type"] = "EXTENDED_NODE"
				row["protocol"] = "PAGP"
			case "duplicate-id":
				rows = append(rows, row)
			case "shared-port":
				copy := map[string]any{"id": 8, "connected_device_type": "TRUNK", "interface_names": []string{"gigabitethernet1/0/1"}}
				rows = append(rows, copy)
			case "missing-id":
				delete(row, "id")
			case "fractional-id":
				row["id"] = 1.5
			case "wrong-type":
				row["connected_device_type"] = "ACCESS"
			case "unknown":
				row["native_id"] = "channel-uuid"
			case "too-many-ports", "pagp-limit":
				ports := []string{}
				limit := 17
				if mode == "pagp-limit" {
					limit = 9
					row["protocol"] = "PAGP"
				}
				for i := 1; i <= limit; i++ {
					ports = append(ports, fmt.Sprintf("GigabitEthernet1/0/%d", i))
				}
				row["interface_names"] = ports
			case "wrong-protocol":
				row["connected_device_type"] = "EXTENDED_NODE"
				row["protocol"] = "LACP"
			case "extended-trunk-fields":
				row["connected_device_type"] = "EXTENDED_NODE"
				row["native_vlan_id"] = 100
			case "bad-vlan":
				row["native_vlan_id"] = 4095
			case "duplicate-port":
				row["interface_names"] = []string{"GigabitEthernet1/0/1", "gigabitethernet1/0/1"}
			}
			encoded, err := json.Marshal(rows)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "empty" {
				encoded = []byte(`[]`)
			}
			if mode == "null" {
				encoded = []byte(`null`)
			}
			raw := portChannelTestModel(string(encoded))
			switch mode {
			case "zone":
				raw = []byte(strings.Replace(string(raw), `"name":"edge"`, `"name":"edge","fabric_zone":"Global/Lab/Building"`, 1))
			case "wrong-role":
				raw = []byte(strings.Replace(string(raw), `"EDGE_NODE"`, `"CONTROL_PLANE_NODE"`, 1))
			case "unprovisioned":
				raw = []byte(strings.Replace(string(raw), `"PROVISION"`, `"ASSIGN"`, 1))
			case "other-effect":
				raw = []byte(strings.Replace(string(raw), `"name":"edge"`, `"name":"edge","port_assignments":[]`, 1))
			}
			devices, channels, err := parseFabricPortChannelDeployment(raw)
			good := mode == "valid" || mode == "options" || mode == "extended" || mode == "zone"
			if !good {
				if err == nil || devices != nil || channels != nil {
					t.Fatal("invalid or partial model accepted")
				}
				return
			}
			if err != nil || len(devices) != 1 || len(channels) != 1 {
				t.Fatal("model", err)
			}
			got := channels[0]
			if *got.channel.ID != 7 || got.channel.Interfaces[0] != "GigabitEthernet1/0/1" || got.device.inventory.reference.Serial != "FOC12345678" {
				t.Fatal("identity lost")
			}
			if mode == "valid" && (got.channel.Protocol != nil || got.channel.Description != nil || got.channel.NativeVLAN != nil || got.channel.AllowedVLANs != nil) {
				t.Fatal("omission lost")
			}
			if mode == "options" && (got.channel.Description == nil || *got.channel.Description != "" || *got.channel.NativeVLAN != 100 || *got.channel.AllowedVLANs != "100,200-210") {
				t.Fatal("options lost")
			}
			if mode == "zone" && got.device.fabricZone != "Global/Lab/Building" {
				t.Fatal("zone lost")
			}
			if _, err := parseFabricDeviceIntents(raw); err == nil {
				t.Fatal("legacy parser discarded port channels")
			}
		})
	}
}

func TestFabricPortChannelPhysicalBindingAndPayload(t *testing.T) {
	for _, mode := range []string{"valid", "options", "missing", "foreign", "virtual", "duplicate-id", "ambiguous", "invalid-fabric"} {
		t.Run(mode, func(t *testing.T) {
			_, intents, err := parseFabricPortChannelDeployment(portChannelTestModel("[" + portChannelTestRow + "]"))
			if err != nil {
				t.Fatal(err)
			}
			want := intents[0].channel
			rows := []lanLinkInterface{{ID: "port-a", DeviceID: "device", PortName: "GigabitEthernet1/0/1", Type: "Physical"}, {ID: "port-b", DeviceID: "device", PortName: "GigabitEthernet1/0/2", Type: "Physical"}}
			fabric := "fabric"
			switch mode {
			case "missing":
				rows = rows[:1]
			case "foreign":
				rows[0].DeviceID = "other"
			case "virtual":
				rows[0].MappedID = "virtual"
			case "duplicate-id":
				rows[1].ID = rows[0].ID
			case "ambiguous":
				rows = append(rows, rows[0])
			case "invalid-fabric":
				fabric = ""
			case "options":
				protocol, description, allowed, vlan := "LACP", "", "100-200", 100
				want.Protocol = &protocol
				want.Description = &description
				want.AllowedVLANs = &allowed
				want.NativeVLAN = &vlan
			}
			body, err := fabricPortChannelPayload("device", fabric, want, rows)
			if mode != "valid" && mode != "options" {
				if err == nil || body != nil {
					t.Fatal("invalid evidence accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			var payload []map[string]any
			if err := json.Unmarshal(body, &payload); err != nil || len(payload) != 1 {
				t.Fatal("payload", err)
			}
			fields := payload[0]
			if fields["id"] != nil || fields["portChannelNumber"] != nil || fields["networkDeviceId"] != "device" || fields["fabricId"] != "fabric" {
				t.Fatal("local identifier leaked", fields)
			}
			if mode == "valid" && len(fields) != 4 {
				t.Fatal("invented defaults", fields)
			}
			if mode == "options" && (len(fields) != 8 || fields["description"] != "" || fields["protocol"] != "LACP") {
				t.Fatal("lost explicit options", fields)
			}
		})
	}
}

func TestFabricPortChannelExample(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/catalyst-center/fabric-port-channel-execution.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var config configapi.NetworkControllerConfig
	if err := yaml.UnmarshalStrict(raw, &config); err != nil {
		t.Fatal(err)
	}
	if !supportedConfigurationApply(&config) {
		t.Fatal("unsupported example")
	}
	devices, channels, err := parseFabricPortChannelDeployment(config.Spec.Source.Inline.Raw)
	if err != nil || len(devices) != 1 || len(channels) != 1 || channels[0].channel.Protocol == nil {
		t.Fatal("incomplete executable example", err)
	}
}
