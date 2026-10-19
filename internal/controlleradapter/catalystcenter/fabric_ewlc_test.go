// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
	"testing"
)

func TestFabricEWLCSettingsAndPayload(t *testing.T) {
	defaults, err := parseFabricEWLCSettings(nil)
	if err != nil || defaults != (fabricEWLCSettings{enabled: true, percentage: 25}) {
		t.Fatal(defaults, err)
	}
	for _, p := range []int{5, 15, 25} {
		settings, err := parseFabricEWLCSettings(map[string]any{"enable_wireless": false, "enable_rolling_ap_upgrade": true, "ap_reboot_percentage": p})
		if err != nil {
			t.Fatal(err)
		}
		body, err := fabricEWLCPayload("device-id", settings)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		want := fmt.Sprintf(`{"id":"device-id","enableWireless":false,"rollingApUpgrade":{"enableRollingApUpgrade":true,"apRebootPercentage":%d}}`, p)
		if string(raw) != want {
			t.Fatal(string(raw))
		}
		var read fabricEWLCSetting
		if err := json.Unmarshal(raw, &read); err != nil || read != body {
			t.Fatal(read, err)
		}
	}
	for _, fields := range []map[string]any{
		{"enable_wireless": nil}, {"enable_wireless": "false"}, {"enable_rolling_ap_upgrade": 0},
		{"ap_reboot_percentage": nil}, {"ap_reboot_percentage": 0}, {"ap_reboot_percentage": 100},
		{"ap_reboot_percentage": 15.5}, {"ap_reboot_percentage": "15"}, {"ap_reboot_percentage": math.Inf(1)},
		{"network_device_id": "id"}, {"enableWireless": true},
	} {
		if _, err := parseFabricEWLCSettings(fields); err == nil {
			t.Fatal("accepted invalid settings", fields)
		}
	}
	for _, id := range []string{"", "../other", "id?query=1"} {
		if _, err := fabricEWLCPath(id); err == nil {
			t.Fatal("accepted bad fabric", id)
		}
		if _, err := fabricEWLCPayload(id, defaults); err == nil {
			t.Fatal("accepted bad device", id)
		}
	}
	if _, err := fabricEWLCPayload("id", fabricEWLCSettings{}); err == nil {
		t.Fatal("accepted unvalidated settings")
	}
}

const fabricEWLCNativeRow = `{"id":"device-id","enableWireless":false,"rollingApUpgrade":{"enableRollingApUpgrade":false,"apRebootPercentage":25}}`

func TestFabricEWLCInventory(t *testing.T) {
	for _, tc := range []struct {
		name, body    string
		status, count int
		bad           bool
	}{
		{"empty", `{"response":[]}`, 200, 0, false},
		{"explicit-false", `{"response":[` + fabricEWLCNativeRow + `]}`, 200, 1, false},
		{"two", `{"response":[` + strings.Replace(fabricEWLCNativeRow, "device-id", "z", 1) + `,` + fabricEWLCNativeRow + `]}`, 200, 2, false},
		{"missing", `{}`, 200, 0, true}, {"null", `{"response":null}`, 200, 0, true},
		{"null-row", `{"response":[null]}`, 200, 0, true},
		{"duplicate", `{"response":[` + fabricEWLCNativeRow + `,` + fabricEWLCNativeRow + `]}`, 200, 0, true},
		{"too-many", `{"response":[` + fabricEWLCNativeRow + `,` + strings.Replace(fabricEWLCNativeRow, "device-id", "b", 1) + `,` + strings.Replace(fabricEWLCNativeRow, "device-id", "c", 1) + `]}`, 200, 0, true},
		{"missing-enabled", `{"response":[` + strings.Replace(fabricEWLCNativeRow, `"enableWireless":false,`, "", 1) + `]}`, 200, 0, true},
		{"missing-rolling", `{"response":[{"id":"device-id","enableWireless":true}]}`, 200, 0, true},
		{"missing-percentage", `{"response":[` + strings.Replace(fabricEWLCNativeRow, `,"apRebootPercentage":25`, "", 1) + `]}`, 200, 0, true},
		{"wrong-percentage", `{"response":[` + strings.Replace(fabricEWLCNativeRow, `:25`, `:30`, 1) + `]}`, 200, 0, true},
		{"unknown-setting", `{"response":[` + strings.Replace(fabricEWLCNativeRow, `"id":`, `"futurePolicy":true,"id":`, 1) + `]}`, 200, 0, true},
		{"unsupported", `{}`, 404, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting" || r.URL.RawQuery != "" {
					t.Error("unexpected request", r.Method, r.URL)
					w.WriteHeader(400)
					return
				}
				reads++
				w.WriteHeader(tc.status)
				fmt.Fprint(w, tc.body)
			})
			rows, err := c.fabricEWLCSettings(context.Background(), "fabric")
			if (err != nil) != tc.bad || (!tc.bad && len(rows) != tc.count) || reads != 1 {
				t.Fatal(rows, err, reads)
			}
			if !tc.bad && tc.count > 0 && (rows[0].ID != "device-id" || rows[0].Enabled || rows[0].Rolling.Enabled) {
				t.Fatal("lost identity or explicit false", rows)
			}
		})
	}
}
