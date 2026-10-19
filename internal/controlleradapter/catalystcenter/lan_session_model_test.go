// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

func lanSessionModelFixture() string {
	return `{"catalyst_center":{"lan_automation":{"devices":[{"name":"session","status":"START","discovered_device_site_name_hierarchy":"Global/Lab","primary_device_management_ip_address":"192.0.2.10","secondary_device_management_ip_address":"192.0.2.11","primary_device_interface_names":["TenGigabitEthernet1/0/1"],"ip_pools":[{"name":"main","role":"PRINCIPAL_IP_ADDRESS_POOL"},{"name":"links","role":"LINK_OVERLAPPING_IP_POOL"}],"multicast_enabled":false,"advertise_lan_automation_routes_into_bgp":true,"discovery_timeout":20,"discovery_devices":[{"device_serial_number":"FOC12345678","device_host_name":"edge","device_site_name_hierarchy":"Global/Lab/Building","device_management_ip_address":"192.0.2.20"}],"isis_domain_password_version":2}]}}}`
}

func TestLANSessionModelProjectionAndPayload(t *testing.T) {
	raw := []byte(lanSessionModelFixture())
	wants, destinations, err := parseLANSessionIntents(raw, false)
	if err != nil || len(wants) != 1 || len(destinations) != 1 || destinations[0].section != "lan_automation" || destinations[0].pointer != "/devices/0/isis_domain_password" {
		t.Fatal("bad metadata model", err)
	}
	if _, err := lanSessionStartPayload(wants[0]); err == nil {
		t.Fatal("missing projected secret accepted")
	}
	snapshot := &configurationSecretSnapshot{values: []configurationSecretValue{{ref: configapi.NetworkControllerSecretRef{Section: destinations[0].section, Path: destinations[0].pointer}, value: []byte("fixture-secret")}}}
	projected, err := snapshot.inject(raw)
	if err != nil {
		t.Fatal(err)
	}
	wants, _, err = parseLANSessionIntents(projected, true)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(fmt.Sprintf("%v %#v", wants, wants), "fixture-secret") {
		t.Fatal("credential formatting leak")
	}
	if _, _, err := parseLANSessionIntents(projected, false); err == nil {
		t.Fatal("inline credential accepted")
	}
	body, err := lanSessionStartPayload(wants[0])
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if json.Unmarshal(body, &rows) != nil || len(rows) != 1 {
		t.Fatal("wrong payload shape")
	}
	got := rows[0]
	if got["primaryDeviceManagmentIPAddress"] != "192.0.2.10" || got["peerDeviceManagmentIPAddress"] != "192.0.2.11" || got["multicastEnabled"] != false || got["redistributeIsisToBgp"] != true || got["discoveryLevel"] != float64(2) || got["discoveryTimeout"] != float64(20) || got["isisDomainPwd"] != "fixture-secret" {
		t.Fatal("wrong payload mapping")
	}
	if got["name"] != nil || got["status"] != nil || got["isis_domain_password_version"] != nil {
		t.Fatal("model metadata leaked onto wire")
	}
	pools := got["ipPools"].([]any)
	if pools[0].(map[string]any)["ipPoolRole"] != "MAIN_POOL" || pools[1].(map[string]any)["ipPoolRole"] != "PHYSICAL_LINK_POOL" {
		t.Fatal("wrong pool mapping")
	}
	d := got["discoveryDevices"].([]any)[0].(map[string]any)
	if d["deviceSerialNumber"] != "FOC12345678" || d["deviceHostName"] != "edge" || d["deviceSiteNameHierarchy"] != "Global/Lab/Building" || d["deviceManagementIPAddress"] != "192.0.2.20" {
		t.Fatal("wrong discovery mapping")
	}
}

func TestLANSessionModelRejectsInvalidIntent(t *testing.T) {
	valid := lanSessionModelFixture()
	for _, change := range [][2]string{
		{`"START"`, `"STOP"`}, {`"START"`, `"unknown"`},
		{`"discovery_timeout":20`, `"discovery_timeout":19`}, {`"discovery_timeout":20`, `"discovery_timeout":10081`},
		{`"discovery_timeout":20`, `"discovery_level":6`}, {`"discovery_timeout":20`, `"discovery_level":1.5`},
		{`"multicast_enabled":false`, `"multicast_enabled":"false"`},
		{`"isis_domain_password_version":2`, `"isis_domain_password_version":0`},
		{`"isis_domain_password_version":2`, `"isis_domain_password":""`},
		{`"PRINCIPAL_IP_ADDRESS_POOL"`, `"MAIN_POOL"`},
		{`"LINK_OVERLAPPING_IP_POOL"`, `"PRINCIPAL_IP_ADDRESS_POOL"`},
		{`"192.0.2.11"`, `"192.0.2.10"`}, {`"192.0.2.20"`, `"192.0.2.10"`},
		{`"Global/Lab/Building"`, `"Global/Laboratory"`},
		{`"FOC12345678"`, `"FOC12345678,FOC87654321"`},
		{`"name":"session"`, `"name":"session","host_name_file_id":"file"`},
		{`"devices":[`, `"links":[],"devices":[`},
	} {
		if _, _, err := parseLANSessionIntents([]byte(strings.Replace(valid, change[0], change[1], 1)), false); err == nil {
			t.Errorf("accepted invalid change to %s", change[0])
		}
	}
	var root map[string]any
	json.Unmarshal([]byte(valid), &root)
	section := root["catalyst_center"].(map[string]any)["lan_automation"].(map[string]any)
	rows := section["devices"].([]any)
	section["devices"] = append(rows, rows[0])
	raw, _ := json.Marshal(root)
	if _, _, err := parseLANSessionIntents(raw, false); err == nil {
		t.Fatal("accepted duplicate sessions")
	}
}
