// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"testing"
)

func TestWirelessInventoryStandaloneDependencies(t *testing.T) {
	for _, scenario := range []string{"ready", "missing site", "missing profile", "ambiguous profile", "unknown profile fields", "replacement serial", "fabric", "unreachable"} {
		t.Run(scenario, func(t *testing.T) {
			s := wirelessInventoryDependencies{devices: []Device{{ID: "wlc", Hostname: "WLC", Serial: "serial", ManagementIP: "198.51.100.10", Family: "Wireless Controller", Reachability: "Reachable"}}, sites: map[string]Site{"Global/Lab": {ID: "lab", Type: "area"}}, profiles: map[string]wirelessProfileMetadata{"profile": {id: "profile-id", name: "profile"}}, profileSites: map[string]map[string]bool{"profile-id": {"lab": true}}}
			desired := inventoryDeviceIntent{reference: configurationDeviceReference{Name: "WLC", Serial: "serial"}, site: "Global/Lab", wireless: &wirelessDevicePlacement{deviceType: "WirelessController", locations: wirelessManagedAPIntent{primary: []string{"Global/Lab"}}}}
			switch scenario {
			case "missing site":
				delete(s.sites, "Global/Lab")
			case "missing profile":
				s.profileSites["profile-id"] = nil
			case "ambiguous profile":
				s.profiles["second"] = wirelessProfileMetadata{id: "second-id", name: "second"}
				s.profileSites["second-id"] = map[string]bool{"lab": true}
			case "unknown profile fields":
				p := s.profiles["profile"]
				p.unmapped = true
				s.profiles["profile"] = p
			case "replacement serial":
				s.devices[0].Serial = "other"
			case "fabric":
				desired.wireless.roles = []string{"WIRELESS_CONTROLLER_NODE"}
			case "unreachable":
				s.devices[0].Reachability = "Unreachable"
			}
			device, bindings, err := s.standalonePlacement(desired)
			if scenario == "ready" {
				if err != nil || device.ID != "wlc" || bindings["profile:profile"] != "profile-id" || bindings["site:Global/Lab"] != "lab" {
					t.Fatal("missing dependency binding", err)
				}
			} else if err == nil {
				t.Fatal("incomplete dependencies accepted")
			}
		})
	}
}

func TestWirelessInventoryDependencyCollection(t *testing.T) {
	for _, stale := range []bool{false, true} {
		t.Run(fmt.Sprint(stale), func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" && r.URL.Path != authPath {
					t.Fatalf("collection mutated %s", r.URL.Path)
				}
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case devicesPath:
					fmt.Fprint(w, `{"response":[{"id":"wlc","hostname":"WLC","serialNumber":"serial","managementIpAddress":"198.51.100.10","family":"Wireless Controller","reachabilityStatus":"Reachable"}]}`)
				case sitesPath:
					fmt.Fprint(w, `{"response":[{"id":"global","name":"Global","nameHierarchy":"Global","type":"global"},{"id":"lab","name":"Lab","nameHierarchy":"Global/Lab","parentId":"global","type":"area"}]}`)
				case wirelessSSIDPath("global"):
					fmt.Fprint(w, `{"response":[]}`)
				case wirelessProfilesPath:
					fmt.Fprint(w, `{"response":[{"id":"profile","wirelessProfileName":"profile","ssidDetails":[]}]}`)
				case profileSiteAssignmentsPath("profile"):
					id := "lab"
					if stale {
						id = "removed-site"
					}
					fmt.Fprintf(w, `{"response":[{"id":%q}]}`, id)
				default:
					t.Errorf("unexpected endpoint %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			snapshot, err := c.wirelessInventoryDependencies(context.Background())
			if stale {
				if err == nil {
					t.Fatal("stale profile assignment accepted")
				}
			} else if err != nil || !snapshot.profileSites["profile"]["lab"] {
				t.Fatal("missing collected assignment", err)
			}
			if !stale {
				desired := inventoryDeviceIntent{reference: configurationDeviceReference{Name: "WLC", Serial: "serial"}, site: "Global/Lab", wireless: &wirelessDevicePlacement{deviceType: "WirelessController", locations: wirelessManagedAPIntent{primary: []string{"Global/Lab"}}}}
				bindings, err := c.validateStandaloneWirelessInventory(context.Background(), []inventoryDeviceIntent{desired})
				if err != nil || bindings["placement:WLC"] == "" || bindings["profile-content:profile"] == "" {
					t.Fatal("complete preflight failed", err)
				}
				if _, err := c.validateStandaloneWirelessInventory(context.Background(), []inventoryDeviceIntent{desired, desired}); err == nil {
					t.Fatal("duplicate physical target accepted")
				}
			}
		})
	}
}

func TestWirelessPlacementProfileContentValidation(t *testing.T) {
	for _, scenario := range []string{"valid", "missing ssid", "replacement ssid", "missing selected profile"} {
		t.Run(scenario, func(t *testing.T) {
			ssidID := "ssid-id"
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case sitesPath:
					fmt.Fprint(w, `{"response":[{"id":"global","name":"Global","nameHierarchy":"Global","type":"global"}]}`)
				case wirelessSSIDPath("global"):
					if scenario == "missing ssid" {
						fmt.Fprint(w, `{"response":[]}`)
					} else {
						fmt.Fprintf(w, `{"response":[{"id":%q,"ssid":"S","authType":"OPEN","wlanType":"Enterprise"}]}`, ssidID)
					}
				default:
					t.Errorf("unexpected dependency request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			snapshot := wirelessInventoryDependencies{profiles: map[string]wirelessProfileMetadata{"P": {name: "P", id: "profile-id", fields: map[string]any{"wirelessProfileName": "P", "ssidDetails": []map[string]any{{"ssidName": "S", "enableFabric": false}}}}}}
			input := map[string]string{"profile:P": "profile-id", "site:Global/Lab": "site-id"}
			if scenario == "missing selected profile" {
				delete(snapshot.profiles, "P")
			}
			first, err := c.validateWirelessPlacementProfiles(context.Background(), snapshot, input)
			if scenario == "missing ssid" || scenario == "missing selected profile" {
				if err == nil {
					t.Fatal("unresolved profile content accepted")
				}
				return
			}
			if err != nil || first["profile-content:P"] == "" || first["site:Global/Lab"] != "site-id" {
				t.Fatal("profile content not bound", err)
			}
			if len(input) != 2 {
				t.Fatal("input bindings modified")
			}
			if scenario == "replacement ssid" {
				ssidID = "replacement-id"
				second, err := c.validateWirelessPlacementProfiles(context.Background(), snapshot, input)
				if err != nil || second["profile-content:P"] == first["profile-content:P"] {
					t.Fatal("SSID replacement not detected", err)
				}
			}
		})
	}
}
