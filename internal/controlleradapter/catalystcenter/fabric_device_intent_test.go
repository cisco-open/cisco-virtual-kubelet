// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

const fabricEdgeIntentModel = `{"catalyst_center":{"inventory":{"devices":[{"name":"edge","serial_number":"FOC12345678","site":"Global/Lab/Building","state":"PROVISION","fabric_site":"Global/Lab","fabric_roles":["EDGE_NODE"]}]}}}`

func TestFabricDeviceIntentModel(t *testing.T) {
	got, err := parseFabricDeviceIntents([]byte(fabricEdgeIntentModel))
	if err != nil || len(got) != 1 || got[0].state != "PROVISION" || got[0].inventory.reference.Serial != "FOC12345678" {
		t.Fatal(got, err)
	}
	for _, replace := range [][2]string{
		{`"PROVISION"`, `"ASSIGN"`}, {`"EDGE_NODE"`, `"EMBEDDED_WIRELESS_CONTROLLER_NODE"`}, {`"EDGE_NODE"`, `"WIRELESS_CONTROLLER_NODE"`},
		{`["EDGE_NODE"]`, `["EDGE_NODE","EDGE_NODE"]`}, {`["EDGE_NODE"]`, `[]`}, {`"EDGE_NODE"`, `"BORDER_NODE"`},
		{`"fabric_site":"Global/Lab"`, `"fabric_site":"Global"`}, {`"fabric_site":"Global/Lab"`, `"fabric_site":"Global/Other"`},
		{`"name":"edge"`, `"name":"edge","device_ip":"not-ip"`}, {`"name":"edge"`, `"name":"edge","networkDeviceId":"uuid"`},
		{`"name":"edge"`, `"name":"edge","fabric_zone":"Global/Other"`}, {`"name":"edge"`, `"name":"edge","port_channels":[]`},
	} {
		raw := strings.Replace(fabricEdgeIntentModel, replace[0], replace[1], 1)
		if _, err := parseFabricDeviceIntents([]byte(raw)); err == nil {
			t.Fatal("accepted invalid intent", raw)
		}
	}
	zone := strings.Replace(fabricEdgeIntentModel, `"name":"edge"`, `"name":"edge","fabric_zone":"Global/Lab/Building"`, 1)
	got, err = parseFabricDeviceIntents([]byte(zone))
	if err != nil || got[0].fabricZone != "Global/Lab/Building" {
		t.Fatal(err)
	}
	var root map[string]any
	json.Unmarshal([]byte(fabricEdgeIntentModel), &root)
	cc := root["catalyst_center"].(map[string]any)
	device := cc["inventory"].(map[string]any)["devices"].([]any)[0].(map[string]any)
	device["fabric_roles"] = []any{"BORDER_NODE", "CONTROL_PLANE_NODE"}
	border := borderFixture()
	border["name"] = "edge"
	cc["fabric"] = map[string]any{"border_devices": []any{border}}
	raw, _ := json.Marshal(root)
	got, err = parseFabricDeviceIntents(raw)
	if err != nil || got[0].border == nil {
		t.Fatal(err)
	}
	border["name"] = "orphan"
	raw, _ = json.Marshal(root)
	if _, err := parseFabricDeviceIntents(raw); err == nil {
		t.Fatal("orphan border accepted")
	}
}
