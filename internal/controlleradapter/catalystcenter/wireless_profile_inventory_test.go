// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestWirelessProfileInventoryProjection(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != wirelessProfilesPath || r.URL.Query().Get("limit") != "500" || r.URL.Query().Get("offset") != "1" {
			t.Error("wrong inventory request")
		}
		fmt.Fprint(w, `{"response":[{"id":"profile-id","wirelessProfileName":"P","additionalInterfaces":["z","a"],"ssidDetails":[{"ssidName":"Z","enableFabric":false,"flexConnect":{"enableFlexConnect":true,"localToVlan":100,"futureSecret":"sentinel-secret"}},{"ssidName":" A# ","dot11beProfileId":"be-id"}],"apZones":[{"apZoneName":"zone","rfProfileName":"rf","ssids":["Z"," A# "]}],"featureTemplates":[{"id":"feature-id","ssids":[]}],"unknownSecret":"sentinel-secret"}]}`)
	})
	profiles, err := c.wirelessProfileInventory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	p := profiles["P"]
	if p.id != "profile-id" || !p.unmapped {
		t.Fatal("identity or unknown-field guard lost")
	}
	raw, _ := json.Marshal(p.fields)
	if strings.Contains(string(raw), "sentinel-secret") || strings.Contains(string(raw), "unknownSecret") || strings.Contains(string(raw), "futureSecret") {
		t.Fatal("unknown data retained")
	}
	if p.fields["additionalInterfaces"].([]string)[0] != "a" || p.fields["ssidDetails"].([]map[string]any)[0]["ssidName"] != " A# " {
		t.Fatal("normalization changed identity or ordering")
	}
	if _, exists := p.fields["id"]; exists {
		t.Fatal("profile ID included in payload metadata")
	}
}
func TestWirelessProfileInventoryRejectsMalformed(t *testing.T) {
	for name, rows := range map[string]string{
		"invalid ID":            `[{"id":"../bad","wirelessProfileName":"P"}]`,
		"duplicate name":        `[{"id":"one","wirelessProfileName":"P"},{"id":"two","wirelessProfileName":"P"}]`,
		"duplicate ID":          `[{"id":"one","wirelessProfileName":"P"},{"id":"one","wirelessProfileName":"Q"}]`,
		"duplicate SSID":        `[{"id":"one","wirelessProfileName":"P","ssidDetails":[{"ssidName":"A"},{"ssidName":"A"}]}]`,
		"missing zone identity": `[{"id":"one","wirelessProfileName":"P","apZones":[{"rfProfileName":"RF"}]}]`,
		"wrong flag":            `[{"id":"one","wirelessProfileName":"P","ssidDetails":[{"ssidName":"A","enableFabric":"false"}]}]`,
		"fractional VLAN":       `[{"id":"one","wirelessProfileName":"P","ssidDetails":[{"ssidName":"A","flexConnect":{"localToVlan":1.5}}]}]`,
		"invalid reference ID":  `[{"id":"one","wirelessProfileName":"P","ssidDetails":[{"ssidName":"A","dot11beProfileId":"../bad"}]}]`,
		"duplicate set":         `[{"id":"one","wirelessProfileName":"P","additionalInterfaces":["I","I"]}]`,
	} {
		t.Run(name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				fmt.Fprintf(w, `{"response":%s}`, rows)
			})
			if _, err := c.wirelessProfileInventory(context.Background()); err == nil {
				t.Fatal("malformed observation accepted")
			}
		})
	}
}
func TestWirelessProfileInventoryPages(t *testing.T) {
	pages := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		pages++
		rows := []map[string]any{}
		switch r.URL.Query().Get("offset") {
		case "1":
			for i := range 500 {
				rows = append(rows, map[string]any{"id": fmt.Sprintf("id-%d", i), "wirelessProfileName": fmt.Sprintf("P%d", i)})
			}
		case "501":
			rows = append(rows, map[string]any{"id": "last", "wirelessProfileName": "last"})
		default:
			t.Error("unexpected offset")
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	rows, err := c.wirelessProfileInventory(context.Background())
	if err != nil || len(rows) != 501 || pages != 2 {
		t.Fatal("incomplete inventory", err, len(rows), pages)
	}
}

// Native profile creation returns this documented SSID attribute even when the
// model did not select it. It must not make a freshly created profile unowned.
func TestWirelessProfileObservedPolicyNamePreserved(t *testing.T) {
	want, refs, actual := wirelessProfilePayloadFixture(t)
	entries := actual.fields["ssidDetails"].([]map[string]any)
	entries[0]["policyProfileName"] = "native-policy"
	body, err := json.Marshal(actual.fields)
	if err != nil {
		t.Fatal(err)
	}
	var native map[string]any
	if json.Unmarshal(body, &native) != nil {
		t.Fatal("fixture")
	}
	observed, unknown, err := projectWirelessProfileObservation(native, wirelessProfileObservation)
	if err != nil || unknown {
		t.Fatal("documented native field rejected", err)
	}
	actual.fields = observed
	payload, err := want.payload(refs, actual)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range payload["ssidDetails"].([]map[string]any) {
		if row["ssidName"] == entries[0]["ssidName"] {
			found = row["policyProfileName"] == "native-policy"
		}
	}
	if !found {
		t.Fatal("unowned policy name lost on full update")
	}
	// The response compatibility exception is not a new model attribute.
	if _, err := parseWirelessProfiles(wirelessProfileTestModel(map[string]any{"name": "P", "ssid_details": []any{map[string]any{"name": "S", "policy_profile_name": "injected"}}})); err == nil {
		t.Fatal("pinned input contract widened")
	}
	native["ssidDetails"].([]any)[0].(map[string]any)["policyProfileName"] = true
	if _, _, err := projectWirelessProfileObservation(native, wirelessProfileObservation); err == nil {
		t.Fatal("malformed native policy name accepted")
	}
}
