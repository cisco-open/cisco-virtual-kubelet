// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSiteLANSessionReferences(t *testing.T) {
	target := Site{ID: siteOwnedTestUUID, NameHierarchy: "Global/Target", Type: "area"}
	sites := map[string]Site{"root": {ID: "root", NameHierarchy: "Global", Type: "global"}, target.ID: target, "child": {ID: "child", NameHierarchy: "Global/Target/Child", Type: "building"}, "other": {ID: "other", NameHierarchy: "Global/Other", Type: "area"}}
	for _, mode := range []string{"empty", "unrelated", "default-device", "same-site", "ancestor", "descendant", "device-id", "device-path", "device-both", "conflict", "unknown-id", "unknown-path", "unknown-session-site", "legacy-scopes", "unknown-session-field", "unknown-device-field", "unknown-action", "stopped", "false-flags", "pending-device-target"} {
		t.Run(mode, func(t *testing.T) {
			s := lanSessionFixture()
			s.Site = "Global/Other"
			devices := []lanSessionDiscoveryDevice{}
			s.DiscoveryDevices = &devices
			sessions := []lanSession{s}
			switch mode {
			case "empty":
				sessions = nil
			case "default-device":
				devices = append(devices, lanSessionDiscoveryDevice{IP: "192.0.2.20"})
			case "same-site", "stopped":
				sessions[0].Site = target.NameHierarchy
			case "ancestor":
				sessions[0].Site = "Global"
			case "descendant":
				sessions[0].Site = "Global/Target/Child"
			case "device-id", "device-both", "conflict", "false-flags":
				d := lanSessionDiscoveryDevice{IP: "192.0.2.20", SiteID: target.ID}
				if mode == "device-both" {
					d.Site = target.NameHierarchy
				}
				if mode == "conflict" {
					d.Site = "Global/Other"
				}
				if mode == "false-flags" {
					value := false
					d.Discovered, d.Allocated, d.Assigned = &value, &value, &value
				}
				devices = append(devices, d)
			case "device-path":
				devices = append(devices, lanSessionDiscoveryDevice{IP: "192.0.2.20", Site: target.NameHierarchy})
			case "pending-device-target":
				devices = append(devices, lanSessionDiscoveryDevice{SiteID: target.ID})
			case "unknown-id":
				devices = append(devices, lanSessionDiscoveryDevice{IP: "192.0.2.20", SiteID: "unknown"})
			case "unknown-path":
				devices = append(devices, lanSessionDiscoveryDevice{IP: "192.0.2.20", Site: "Global/Unknown"})
			case "unknown-session-site":
				sessions[0].Site = "Global/Unknown"
			case "legacy-scopes":
				sessions[0].DiscoveryDevices = nil
			case "unknown-session-field":
				sessions[0].unmapped = true
			case "unknown-action":
				sessions[0].Action = "FUTURE_ACTION"
			case "unknown-device-field":
				devices = append(devices, lanSessionDiscoveryDevice{IP: "192.0.2.20", unmapped: true})
			}
			before := map[string]Site{}
			for id, s := range sites {
				before[id] = s
			}
			err := checkSiteLANSessionReferences(target, sites, sessions)
			if (err == nil) != (mode == "empty" || mode == "unrelated" || mode == "default-device") {
				t.Fatal("LAN site reference authority mismatch", mode, err)
			}
			if !reflect.DeepEqual(before, sites) {
				t.Fatal("LAN reference check changed site inventory")
			}
		})
	}
}

func TestLANSessionDiscoveryScopeMetadata(t *testing.T) {
	for _, mode := range []string{"valid", "empty", "missing", "pending-address", "unknown-session", "unknown-device", "bad-ip", "bad-site", "bad-site-id", "duplicate-ip", "duplicate-serial", "duplicate-pnp", "too-many", "invalid-array"} {
		t.Run(mode, func(t *testing.T) {
			s := lanSessionFixture()
			devices := []lanSessionDiscoveryDevice{{IP: "192.0.2.20", Serial: "FOC12345678", Name: "edge", SiteID: "area", Site: "Global/Lab", PnPID: "pnp"}}
			s.DiscoveryDevices = &devices
			switch mode {
			case "empty":
				devices = nil
				devices = append([]lanSessionDiscoveryDevice{}, devices...)
			case "missing":
				s.DiscoveryDevices = nil
			case "pending-address":
				devices[0].IP = ""
			case "bad-ip":
				devices[0].IP = "seed.example"
			case "bad-site":
				devices[0].Site = "unrooted"
			case "bad-site-id":
				devices[0].SiteID = "bad/id"
			case "duplicate-ip":
				devices = append(devices, devices[0])
			case "duplicate-serial", "duplicate-pnp":
				copy := devices[0]
				copy.IP = "192.0.2.21"
				if mode == "duplicate-serial" {
					copy.PnPID = "other"
				} else {
					copy.Serial = "FOC12345679"
				}
				devices = append(devices, copy)
			case "too-many":
				for len(devices) < 51 {
					devices = append(devices, devices[0])
				}
			}
			raw, _ := json.Marshal(s)
			var body map[string]any
			json.Unmarshal(raw, &body)
			if mode == "unknown-session" {
				body["futureSiteScope"] = "Global/Other"
			}
			if mode == "unknown-device" {
				body["discoveryDevices"].([]any)[0].(map[string]any)["futureSiteId"] = "other"
			}
			if mode == "invalid-array" {
				body["discoveryDevices"] = map[string]any{}
			}
			raw, _ = json.Marshal(body)
			var decoded lanSession
			err := json.Unmarshal(raw, &decoded)
			if err == nil {
				err = normalizeLANSession(&decoded)
			}
			valid := mode == "valid" || mode == "empty" || mode == "missing" || mode == "pending-address" || mode == "unknown-session" || mode == "unknown-device"
			if (err == nil) != valid {
				t.Fatal("LAN discovery metadata decode mismatch", err)
			}
			if mode == "unknown-session" && !decoded.unmapped {
				t.Fatal("unknown session scope discarded")
			}
			if mode == "unknown-device" && !(*decoded.DiscoveryDevices)[0].unmapped {
				t.Fatal("unknown device scope discarded")
			}
			if mode == "valid" && (*decoded.DiscoveryDevices)[0].Serial != "foc12345678" {
				t.Fatal("physical identity not canonical")
			}
		})
	}
}
