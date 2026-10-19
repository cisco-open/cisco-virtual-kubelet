// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFabricEWLCIntent(t *testing.T) {
	raw := strings.Replace(fabricEdgeIntentModel, `["EDGE_NODE"]`, `["EDGE_NODE","EMBEDDED_WIRELESS_CONTROLLER_NODE"]`, 1)
	got, err := parseFabricEWLCIntents([]byte(raw))
	if err != nil || len(got) != 1 || got[0].device.inventory.reference.Serial != "FOC12345678" || !got[0].settings.enabled || got[0].settings.percentage != 25 || got[0].locations != nil {
		t.Fatal(got, err)
	}
	embeddedOnly := strings.Replace(raw, `"EDGE_NODE",`, ``, 1)
	inherited, err := parseFabricEWLCIntents([]byte(embeddedOnly))
	if err != nil || len(inherited) != 1 || len(inherited[0].device.roles) != 0 {
		t.Fatal("embedded-only roles", inherited, err)
	}
	if _, err := parseFabricDeviceIntents([]byte(strings.Replace(fabricEdgeIntentModel, `["EDGE_NODE"]`, `[]`, 1))); err == nil {
		t.Fatal("base creation accepted missing roles")
	}
	withAP := strings.Replace(raw, `"name":"edge"`, `"name":"edge","enable_wireless":false,"enable_rolling_ap_upgrade":true,"ap_reboot_percentage":15,"primary_managed_ap_locations":["Global/Lab/Building"]`, 1)
	got, err = parseFabricEWLCIntents([]byte(withAP))
	if err != nil || got[0].settings.enabled || !got[0].settings.rolling || got[0].settings.percentage != 15 || got[0].locations == nil || len(got[0].locations.primary) != 1 {
		t.Fatal(got, err)
	}
	for _, change := range [][2]string{
		{`"EMBEDDED_WIRELESS_CONTROLLER_NODE"`, `"WIRELESS_CONTROLLER_NODE"`},
		{`"EMBEDDED_WIRELESS_CONTROLLER_NODE"`, `"EMBEDDED_WIRELESS_CONTROLLER_NODE","EMBEDDED_WIRELESS_CONTROLLER_NODE"`},
		{`"EDGE_NODE"`, `"UNKNOWN"`},
		{`"PROVISION"`, `"ASSIGN"`},
		{`"name":"edge"`, `"name":"edge","enable_wireless":null`},
		{`"name":"edge"`, `"name":"edge","primary_managed_ap_locations":null`},
		{`"name":"edge"`, `"name":"edge","primary_managed_ap_locations":["Global"]`},
		{`"name":"edge"`, `"name":"edge","anchor_managed_ap_locations":["Global/Lab"]`},
		{`"name":"edge"`, `"name":"edge","unexpected":true`},
		{`"fabric_site":"Global/Lab"`, `"fabric_site":"Global/Elsewhere"`},
	} {
		bad := strings.Replace(raw, change[0], change[1], 1)
		if _, err := parseFabricEWLCIntents([]byte(bad)); err == nil {
			t.Fatal("accepted invalid embedded intent", bad)
		}
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(withAP), &root); err != nil {
		t.Fatal(err)
	}
	cc := root["catalyst_center"].(map[string]any)
	inventory := cc["inventory"].(map[string]any)
	first := inventory["devices"].([]any)[0].(map[string]any)
	second := map[string]any{}
	for k, v := range first {
		second[k] = v
	}
	second["name"] = "aaa"
	second["serial_number"] = "FOC87654321"
	second["enable_wireless"] = true
	second["ap_reboot_percentage"] = 5
	inventory["devices"] = []any{first, second}
	encoded, _ := json.Marshal(root)
	got, err = parseFabricEWLCIntents(encoded)
	if err != nil || len(got) != 2 || got[0].device.inventory.reference.Name != "aaa" || got[0].settings.percentage != 5 || got[1].settings.percentage != 15 {
		t.Fatal("sorted identity lost settings", got, err)
	}
	second["name"] = "edge"
	encoded, _ = json.Marshal(root)
	if _, err := parseFabricEWLCIntents(encoded); err == nil {
		t.Fatal("duplicate names accepted")
	}
}
