// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestWirelessProfileReferenceCollection(t *testing.T) {
	for _, collision := range []bool{false, true} {
		t.Run(fmt.Sprint(collision), func(t *testing.T) {
			profiles, err := parseWirelessProfiles(wirelessProfileTestModel(map[string]any{"name": "P", "ssid_details": []any{map[string]any{"name": "S"}}, "feature_templates": []any{"CleanAir", "FRA"}}))
			if err != nil {
				t.Fatal(err)
			}
			calls := map[string]int{}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls[r.URL.Path]++
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}})
				case wirelessSSIDPath("global"):
					fmt.Fprint(w, `{"response":[{"id":"ssid-id","ssid":"S","authType":"OPEN","wlanType":"Enterprise"}]}`)
				case wirelessInterfacesPath:
					fmt.Fprint(w, `{"response":[{"id":"interface-id","interfaceName":"management","vlanId":0}]}`)
				case wirelessFeatureSummaryPath:
					kind := r.URL.Query().Get("type")
					name, id := "CleanAir", "clean-id"
					if kind == wirelessFRAKind {
						id = "fra-id"
						if !collision {
							name = "FRA"
						}
					} else if kind != wirelessCleanAirKind {
						t.Error("unknown feature kind")
					}
					json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"type": kind, "count": 1, "instances": []any{map[string]any{"id": id, "designName": name, "systemTemplate": false}}}}})
				default:
					t.Errorf("unneeded inventory %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			refs, err := c.wirelessProfileReferences(context.Background(), profiles[0], nil)
			if collision {
				if err == nil {
					t.Fatal("ambiguous name accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			payload, err := profiles[0].payload(refs, nil)
			if err != nil || len(payload["featureTemplates"].([]map[string]any)) != 2 {
				t.Fatal("binding failed", err)
			}
			if calls[wirelessFeatureSummaryPath] != 2 || calls[wirelessInterfacesPath] != 1 || calls[wirelessSSIDPath("global")] != 1 {
				t.Fatal("unexpected inventory calls", calls)
			}
		})
	}
}
func TestWirelessProfileReferenceCollectionDropsReplacedVLAN(t *testing.T) {
	profiles, err := parseWirelessProfiles(wirelessProfileTestModel(map[string]any{"name": "P", "ssid_details": []any{map[string]any{"name": "S", "enable_fabric": true}}}))
	if err != nil {
		t.Fatal(err)
	}
	actual := &wirelessProfileMetadata{id: "profile-id", name: "P", fields: map[string]any{"ssidDetails": []map[string]any{{"ssidName": "S", "vlanGroupName": "retired"}}}}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": []Site{{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"}}})
		case wirelessSSIDPath("global"):
			fmt.Fprint(w, `{"response":[{"id":"ssid-id","ssid":"S","authType":"OPEN","wlanType":"Enterprise"}]}`)
		default:
			t.Errorf("removed dependency queried %s", r.URL.Path)
			w.WriteHeader(404)
		}
	})
	refs, err := c.wirelessProfileReferences(context.Background(), profiles[0], actual)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = profiles[0].payload(refs, actual); err != nil {
		t.Fatal(err)
	}
	profiles[0].ssids = nil
	refs, err = c.wirelessProfileReferences(context.Background(), profiles[0], actual)
	if err != nil {
		t.Fatal("named VLAN selector required an invented inventory", err)
	}
	body, err := profiles[0].payload(refs, actual)
	if err != nil || body["ssidDetails"].([]map[string]any)[0]["vlanGroupName"] != "retired" {
		t.Fatal("retained named selector lost", err)
	}
	if _, err = wirelessProfileBinding(body, refs); err != nil {
		t.Fatal("named selector did not bind", err)
	}
}
