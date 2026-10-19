// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestWirelessZoneBindingV4EncodingContract(t *testing.T) {
	for _, tc := range []struct {
		name  string
		graph wirelessZoneBindings
		wire  string
	}{
		{"references", wirelessZoneBindings{ParentID: "parent", GlobalSiteID: "global", Zones: map[string]wirelessZoneBinding{"Z": {RF: "RF", SSIDs: []string{"S"}}}, RFIDs: map[string]string{"RF": "rf-id"}, SSIDIDs: map[string]string{"S": "ssid-id"}}, `{"ParentID":"parent","GlobalSiteID":"global","Zones":{"Z":{"Builtin":false,"RF":"RF","SSIDs":["S"]}},"RFIDs":{"RF":"rf-id"},"SSIDIDs":{"S":"ssid-id"}}`},
		{"empty", wirelessZoneBindings{ParentID: "parent", Zones: map[string]wirelessZoneBinding{}, RFIDs: map[string]string{}, SSIDIDs: map[string]string{}}, `{"ParentID":"parent","GlobalSiteID":"","Zones":{},"RFIDs":{},"SSIDIDs":{}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.graph)
			if err != nil || string(encoded) != tc.wire {
				t.Fatal("v4 canonical encoding changed; explicit protocol migration required", err)
			}
		})
	}
}

func TestWirelessProfileZoneBindings(t *testing.T) {
	for _, mode := range []string{"selected", "all", "empty", "builtin", "no-rf", "no-ssid", "missing-rf", "missing-ssid", "foreign-ssid", "sensor-ssid", "missing-global", "rf-read-error", "ssid-read-error", "sites-read-error", "unknown-zone", "duplicate-selection", "unknown-parent", "unknown-zone-field", "duplicate-zone", "bad-rf-type", "bad-ssid-type", "duplicate-ssid", "orphan-ssid", "duplicate-parent-ssid", "bad-parent-ssid-type"} {
		t.Run(mode, func(t *testing.T) {
			zone := map[string]any{"apZoneName": "Z1", "rfProfileName": "RF1", "ssids": []string{" A# "}}
			profile := wirelessProfileMetadata{id: "profile-id", name: "P", fields: map[string]any{
				"apZones":     []map[string]any{zone, {"apZoneName": "Z2", "rfProfileName": "RF2", "ssids": []string{"Other"}}},
				"ssidDetails": []map[string]any{{"ssidName": " A# "}, {"ssidName": "Other"}},
			}}
			selected := []string{"Z1"}
			switch mode {
			case "all":
				selected = nil
			case "empty":
				selected = []string{}
			case "builtin":
				selected = []string{"default-zone"}
			case "no-rf":
				delete(zone, "rfProfileName")
			case "no-ssid":
				zone["ssids"] = []string{}
			case "unknown-zone":
				selected = []string{"missing"}
			case "duplicate-selection":
				selected = []string{"Z1", "Z1"}
			case "unknown-parent":
				profile.unmapped = true
			case "unknown-zone-field":
				zone["future"] = "secret-sentinel"
			case "duplicate-zone":
				profile.fields["apZones"] = []map[string]any{zone, zone}
			case "bad-rf-type":
				zone["rfProfileName"] = true
			case "bad-ssid-type":
				zone["ssids"] = []any{" A# "}
			case "duplicate-ssid":
				zone["ssids"] = []string{" A# ", " A# "}
			case "orphan-ssid":
				zone["ssids"] = []string{"Unattached"}
			case "duplicate-parent-ssid":
				profile.fields["ssidDetails"] = []map[string]any{{"ssidName": " A# "}, {"ssidName": " A# "}}
			case "bad-parent-ssid-type":
				profile.fields["ssidDetails"] = []any{}
			}
			var mu sync.Mutex
			reads := map[string]int{}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != http.MethodGet && r.URL.Path != authPath {
					t.Error("identity collector attempted a mutation")
					w.WriteHeader(500)
					return
				}
				reads[r.URL.Path]++
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case wirelessRFPath:
					if mode == "rf-read-error" {
						w.WriteHeader(503)
						return
					}
					rows := []any{map[string]any{"id": "rf-two", "rfProfileName": "RF2", "defaultRfProfile": false, "enableRadioTypeA": true, "enableRadioTypeB": false, "enableRadioType6GHz": false}}
					if mode != "missing-rf" {
						rows = append(rows, map[string]any{"id": "rf-one", "rfProfileName": "RF1", "defaultRfProfile": false, "enableRadioTypeA": true, "enableRadioTypeB": false, "enableRadioType6GHz": false, "futureField": "secret-sentinel"})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case sitesPath:
					if mode == "sites-read-error" {
						w.WriteHeader(503)
						return
					}
					rows := []Site{}
					if mode != "missing-global" {
						rows = append(rows, Site{ID: "global", Name: "Global", NameHierarchy: "Global", Type: "global"})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
				case wirelessSSIDPath("global"):
					if mode == "ssid-read-error" {
						w.WriteHeader(503)
						return
					}
					rows := []any{map[string]any{"id": "ssid-two", "ssid": "Other", "authType": "OPEN", "wlanType": "Enterprise"}}
					if mode != "missing-ssid" {
						row := map[string]any{"id": "ssid-one", "ssid": " A# ", "passphrase": "secret-sentinel", "inheritedSiteId": "global", "authType": "WPA2_PERSONAL", "wlanType": "Enterprise"}
						if mode == "foreign-ssid" {
							row["inheritedSiteId"] = "another-site"
						}
						if mode == "sensor-ssid" {
							row["isSensorPnp"] = true
						}
						rows = append(rows, row)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
				default:
					t.Error("unexpected reader", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			before, _ := json.Marshal(profile.fields)
			got, err := c.wirelessProfileZoneBindings(context.Background(), profile, selected)
			valid := mode == "selected" || mode == "all" || mode == "empty" || mode == "builtin" || mode == "no-rf" || mode == "no-ssid"
			if (err == nil) != valid {
				t.Fatal("binding outcome differs", err)
			}
			after, _ := json.Marshal(profile.fields)
			if string(before) != string(after) {
				t.Fatal("reader mutated native metadata")
			}
			raw, _ := json.Marshal(got)
			if strings.Contains(string(raw), "secret-sentinel") || strings.Contains(string(raw), "passphrase") {
				t.Fatal("binding retained unrelated/secret native fields")
			}
			mu.Lock()
			defer mu.Unlock()
			if !valid {
				owned := mode == "missing-rf" || mode == "missing-ssid" || mode == "foreign-ssid" || mode == "sensor-ssid" || mode == "unknown-zone" || mode == "unknown-parent" || mode == "unknown-zone-field"
				if owned && !errors.Is(err, errConfigurationOwned) {
					t.Fatal("fixture failed before its intended identity fence", err)
				}
				offline := strings.HasPrefix(mode, "bad-") || strings.HasPrefix(mode, "duplicate-") || strings.HasPrefix(mode, "unknown-") || mode == "orphan-ssid"
				if offline && len(reads) != 0 {
					t.Fatal("malformed source performed unnecessary native reads")
				}
				if (mode == "missing-ssid" || mode == "foreign-ssid" || mode == "sensor-ssid" || mode == "ssid-read-error") && reads[wirelessSSIDPath("global")] != 1 {
					t.Fatal("SSID negative case never reached the intended reader")
				}
				return
			}
			if mode == "empty" || mode == "builtin" {
				if len(reads) != 0 || len(got.RFIDs) != 0 || len(got.SSIDIDs) != 0 {
					t.Fatal("invented built-in UUID or unnecessary inventory read")
				}
				if mode == "builtin" && !got.Zones["default-zone"].Builtin {
					t.Fatal("built-in identity lost")
				}
				return
			}
			if mode != "no-rf" && (got.RFIDs["RF1"] != "rf-one" || reads[wirelessRFPath] != 1) {
				t.Fatal("RF UUID missing or repeated inventory")
			}
			if mode != "no-ssid" && (got.SSIDIDs[" A# "] != "ssid-one" || got.GlobalSiteID != "global" || reads[wirelessSSIDPath("global")] != 1) {
				t.Fatal("SSID identity was normalized or not resolved once")
			}
			if mode == "all" && (got.RFIDs["RF2"] != "rf-two" || got.SSIDIDs["Other"] != "ssid-two") {
				t.Fatal("whole-parent references incomplete")
			}
			if mode != "all" && (got.RFIDs["RF2"] != "" || got.SSIDIDs["Other"] != "") {
				t.Fatal("unselected references widened the binding")
			}
			if mode == "no-rf" && reads[wirelessRFPath] != 0 || mode == "no-ssid" && (reads[sitesPath] != 0 || reads[wirelessSSIDPath("global")] != 0) {
				t.Fatal("read an unused inventory class")
			}
		})
	}
}

func TestWirelessZoneBindingDetectsSameNameReplacement(t *testing.T) {
	for _, kind := range []string{"rf", "ssid", "order"} {
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			rfID, ssidID := "rf-original", "ssid-original"
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					fmt.Fprint(w, `{"response":[{"id":"global","name":"Global","nameHierarchy":"Global","type":"global"}]}`)
				case wirelessRFPath:
					fmt.Fprintf(w, `{"response":[{"id":%q,"rfProfileName":"RF","defaultRfProfile":false,"enableRadioTypeA":true,"enableRadioTypeB":false,"enableRadioType6GHz":false}]}`, rfID)
				case wirelessSSIDPath("global"):
					fmt.Fprintf(w, `{"response":[{"id":%q,"ssid":"S","authType":"OPEN","wlanType":"Enterprise"},{"id":"ssid-other","ssid":"T","authType":"OPEN","wlanType":"Enterprise"}]}`, ssidID)
				default:
					t.Error("unexpected read")
					w.WriteHeader(404)
				}
			})
			p := wirelessProfileMetadata{id: "parent", name: "P", fields: map[string]any{"apZones": []map[string]any{{"apZoneName": "Z", "rfProfileName": "RF", "ssids": []string{"S", "T"}}}, "ssidDetails": []map[string]any{{"ssidName": "S"}, {"ssidName": "T"}}}}
			first, err := c.wirelessProfileZoneBindings(context.Background(), p, nil)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			if kind == "rf" {
				rfID = "rf-replaced"
			} else if kind == "ssid" {
				ssidID = "ssid-replaced"
			}
			mu.Unlock()
			if kind == "order" {
				p.fields["apZones"].([]map[string]any)[0]["ssids"] = []string{"T", "S"}
			}
			second, err := c.wirelessProfileZoneBindings(context.Background(), p, nil)
			if err != nil || reflect.DeepEqual(first, second) != (kind == "order") {
				t.Fatal("same-name dependency replacement escaped identity snapshot", err)
			}
			a, _ := wirelessTagDigest(first)
			b, _ := wirelessTagDigest(second)
			if (a == b) != (kind == "order") {
				t.Fatal("replacement did not change canonical binding digest")
			}
		})
	}
}
