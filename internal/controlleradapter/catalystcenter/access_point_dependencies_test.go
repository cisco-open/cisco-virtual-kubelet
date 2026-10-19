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

func TestAccessPointProvisionDependencies(t *testing.T) {
	model := map[string]any{"name": "AP1", "serial_number": "SERIAL", "type": "AccessPoint", "site": "Global/Lab/Building/Floor", "state": "PROVISION", "rf_profile": "TYPICAL"}
	want, err := parseAccessPointDevice(model, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"assigned", "not-assigned", "missing-rf", "unknown-rf", "wrong-site-assignment", "replaced-rf", "changed-rf", "replaced-floor", "replaced-ap", "changed-mac"} {
		t.Run(mode, func(t *testing.T) {
			revision := false
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("dependency observation mutated controller")
				}
				rows := []any{}
				switch r.URL.Path {
				case devicesPath:
					device := Device{ID: "ap", Hostname: "AP1", Serial: "SERIAL", PlatformID: "C9130AXI", APEthernetMAC: "02:00:00:00:00:01"}
					if revision && mode == "replaced-ap" {
						device.ID = "new-ap"
					}
					if revision && mode == "changed-mac" {
						device.APEthernetMAC = "02:00:00:00:00:02"
					}
					rows = append(rows, device)
				case sitesPath:
					sites := sampleSites()
					id := "floor"
					if revision && mode == "replaced-floor" {
						id = "new-floor"
					}
					sites = append(sites, Site{ID: id, Name: "Floor", NameHierarchy: want.inventory.site, ParentID: "building", Type: "floor"})
					for _, site := range sites {
						rows = append(rows, site)
					}
				case wirelessRFPath:
					rf := map[string]any{"id": "rf", "rfProfileName": "TYPICAL", "defaultRfProfile": true, "enableRadioTypeA": true, "enableRadioTypeB": true, "enableRadioType6GHz": false}
					if mode == "unknown-rf" {
						rf["unqualifiedSetting"] = true
					}
					if revision && mode == "replaced-rf" {
						rf["id"] = "new-rf"
					}
					if revision && mode == "changed-rf" {
						rf["enableRadioTypeB"] = false
					}
					if mode != "missing-rf" {
						rows = append(rows, rf)
					}
				case assignedDevicesPath:
					id := "ap"
					if revision && mode == "replaced-ap" {
						id = "new-ap"
					}
					site := r.URL.Query().Get("siteId")
					if mode == "wrong-site-assignment" {
						site = "other"
					}
					if mode != "not-assigned" {
						rows = append(rows, map[string]any{"deviceId": id, "siteId": site})
					}
				default:
					t.Error("unexpected endpoint")
					w.WriteHeader(404)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			first, err := c.accessPointProvisionDependencies(context.Background(), want)
			invalid := mode == "missing-rf" || mode == "unknown-rf" || mode == "wrong-site-assignment"
			if invalid {
				if err == nil || first.digest != "" {
					t.Fatal("invalid dependencies returned usable evidence")
				}
				return
			}
			if err != nil || first.assigned != (mode != "not-assigned") || first.siteID != "floor" || first.rfID != "rf" || first.digest == "" {
				t.Fatal("incorrect dependencies", err)
			}
			revision = true
			second, err := c.accessPointProvisionDependencies(context.Background(), want)
			if err != nil {
				t.Fatal(err)
			}
			changed := mode != "assigned" && mode != "not-assigned"
			if (first.digest != second.digest) != changed {
				t.Fatal("dependency replacement/content change not reflected in digest")
			}
		})
	}
}
