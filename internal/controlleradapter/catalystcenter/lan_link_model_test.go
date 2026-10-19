// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func lanLinkFixture() map[string]any {
	return map[string]any{
		"name": "seed-peer", "action": "ADD_LINK", "ip_pool_name": "underlay",
		"primary_device_management_ip_address": "192.0.2.10", "primary_device_interface_name": "TenGigabitEthernet1/0/1",
		"secondary_device_management_ip_address": "192.0.2.11", "secondary_device_interface_name": "TenGigabitEthernet1/0/2",
	}
}

func lanLinkModel(t *testing.T, rows ...map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"lan_automation": map[string]any{"links": rows}}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestLANLinkModelAndWireMapping(t *testing.T) {
	for _, action := range []string{"ADD_LINK", "DELETE_LINK"} {
		t.Run(action, func(t *testing.T) {
			row := lanLinkFixture()
			row["action"] = action
			if action == "DELETE_LINK" {
				delete(row, "ip_pool_name")
			}
			wants, err := parseLANLinks(lanLinkModel(t, row))
			if err != nil || len(wants) != 1 {
				t.Fatal(wants, err)
			}
			path, body, err := lanLinkPayload(wants[0])
			feature := "LINK_ADD"
			if action == "DELETE_LINK" {
				feature = "LINK_DELETE"
			}
			if err != nil || path != "/dna/intent/api/v1/lan-automation/updateDevice?feature="+feature {
				t.Fatal(path, err)
			}
			var actual map[string]map[string]string
			if err := json.Unmarshal(body, &actual); err != nil {
				t.Fatal(err)
			}
			expected := map[string]string{
				"sourceDeviceManagementIPAddress": "192.0.2.10", "sourceDeviceInterfaceName": "TenGigabitEthernet1/0/1",
				"destinationDeviceManagementIPAddress": "192.0.2.11", "destinationDeviceInterfaceName": "TenGigabitEthernet1/0/2",
			}
			if action == "ADD_LINK" {
				expected["ipPoolName"] = "underlay"
			}
			if len(actual) != 1 || !reflect.DeepEqual(actual["linkUpdate"], expected) {
				t.Fatalf("wrong native mapping: %s", body)
			}
		})
	}
}

func TestLANLinkRejectsAmbiguousIntent(t *testing.T) {
	for _, field := range []string{"name", "action", "ip_pool_name", "primary_device_management_ip_address", "primary_device_interface_name", "secondary_device_management_ip_address", "secondary_device_interface_name"} {
		t.Run("missing-"+field, func(t *testing.T) {
			row := lanLinkFixture()
			delete(row, field)
			if _, err := parseLANLinks(lanLinkModel(t, row)); err == nil {
				t.Fatal("accepted missing required field")
			}
		})
	}
	for field, values := range map[string][]any{
		"action":                               {"START", "add_link", nil, true},
		"primary_device_management_ip_address": {"seed.example", "::ffff:192.0.2.10", "0.0.0.0", "224.0.0.1", "192.0.2.11", "192.0.2.10 "},
		"primary_device_interface_name":        {"", " Gi1/0/1", "Gi1/0/1\n", strings.Repeat("x", 257)},
		"peer_device_interface_name":           {"Gi1/0/1"},
		"ip_pool_name":                         {nil, 42, ""},
	} {
		for _, value := range values {
			row := lanLinkFixture()
			row[field] = value
			if _, err := parseLANLinks(lanLinkModel(t, row)); err == nil {
				t.Errorf("accepted invalid %s", field)
			}
		}
	}
	for _, mode := range []string{"duplicate-name", "shared-port", "reverse-conflict", "distinct"} {
		t.Run(mode, func(t *testing.T) {
			first, second := lanLinkFixture(), lanLinkFixture()
			second["name"] = "another"
			switch mode {
			case "duplicate-name":
				second["name"] = first["name"]
				second["primary_device_management_ip_address"] = "192.0.2.12"
				second["secondary_device_management_ip_address"] = "192.0.2.13"
			case "reverse-conflict":
				second["action"] = "DELETE_LINK"
				second["primary_device_management_ip_address"], second["secondary_device_management_ip_address"] = first["secondary_device_management_ip_address"], first["primary_device_management_ip_address"]
				second["primary_device_interface_name"], second["secondary_device_interface_name"] = first["secondary_device_interface_name"], first["primary_device_interface_name"]
			case "distinct":
				second["primary_device_interface_name"] = "TenGigabitEthernet1/0/3"
				second["secondary_device_interface_name"] = "TenGigabitEthernet1/0/4"
			}
			got, err := parseLANLinks(lanLinkModel(t, first, second))
			if (err == nil) != (mode == "distinct") {
				t.Fatal(got, err)
			}
			if err == nil && (len(got) != 2 || got[0].name != "another") {
				t.Fatal("unstable ordering")
			}
		})
	}
	for _, raw := range []string{
		`{"catalyst_center":{"lan_automation":{"links":[]}}}`,
		`{"catalyst_center":{"lan_automation":{"links":null}}}`,
		strings.Replace(string(lanLinkModel(t, lanLinkFixture())), `"links":`, `"devices":[],"links":`, 1),
		strings.Replace(string(lanLinkModel(t, lanLinkFixture())), `"lan_automation":`, `"sites":[],"lan_automation":`, 1),
	} {
		if _, err := parseLANLinks([]byte(raw)); err == nil {
			t.Fatal("accepted partial projection")
		}
	}
	if _, _, err := lanLinkPayload(lanLinkIntent{action: "ADD_LINK"}); err == nil {
		t.Fatal("payload builder bypassed validation")
	}
}
