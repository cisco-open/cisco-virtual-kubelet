// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"testing"
)

func TestFabricVLANSSIDDependencies(t *testing.T) {
	for _, mode := range []string{"ready", "missing-edge", "foreign-member", "replaced-site", "retained-unknown-vlan", "retained-unknown-ssid", "profile-error", "model-not-started", "changed-profile", "changed-stamp", "changed-tag", "changed-edge"} {
		t.Run(mode, func(t *testing.T) {
			wireless := true
			want := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}}}
			tag := "Guests"
			state := &fabricVLANSSIDObservation{siteID: "site", fabricID: "fabric", mappings: []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Guest", SecurityGroup: &tag}}}}}
			members := map[string]fabricDeviceMembership{
				"wlc":  {ID: "wlc-member", NetworkDeviceID: "wlc", FabricID: "fabric", Roles: []string{"WIRELESS_CONTROLLER_NODE"}},
				"edge": {ID: "edge-member", NetworkDeviceID: "edge", FabricID: "fabric", Roles: []string{"EDGE_NODE"}},
			}
			snapshot := wirelessInventoryDependencies{
				devices:      []Device{{ID: "wlc", Serial: "FOC12345678", Hostname: "controller", Family: "Wireless Controller", Reachability: "Reachable"}, {ID: "edge", Serial: "FOC87654321", Hostname: "edge", Family: "Switches and Hubs", Reachability: "Reachable"}},
				sites:        map[string]Site{"Global/Lab": {ID: "site", NameHierarchy: "Global/Lab"}},
				profiles:     map[string]wirelessProfileMetadata{"Wireless": {id: "profile", name: "Wireless", fields: map[string]any{"ssidDetails": []map[string]any{{"ssidName": "Staff", "enableFabric": true}, {"ssidName": "Guest", "enableFabric": true}}}}},
				profileSites: map[string]map[string]bool{"profile": {"site": true}},
			}
			l2 := map[fabricL2Key]fabricL2Network{{"fabric", "Clients"}: {ID: "l2", FabricID: "fabric", VLANName: "Clients", VLAN: 100, Traffic: "DATA", Wireless: &wireless}}
			switch mode {
			case "missing-edge":
				snapshot.devices = snapshot.devices[:1]
			case "foreign-member":
				row := members["edge"]
				row.FabricID = "other"
				members["edge"] = row
			case "replaced-site":
				state.siteID = "replacement"
			case "retained-unknown-vlan":
				state.mappings[0].VLAN = "Other"
			case "retained-unknown-ssid":
				state.mappings[0].SSIDs[0].Name = "Other"
			}
			revised := false
			collect := func() (*fabricVLANSSIDDependencies, error) {
				return resolveFabricVLANSSIDDependencies("Global/Lab", want, nil, state, members, snapshot, l2, nil,
					func(string) (map[string]wirelessSSIDMetadata, error) {
						return map[string]wirelessSSIDMetadata{"Staff": {id: "staff", name: "Staff"}, "Guest": {id: "guest", name: "Guest"}}, nil
					},
					func(string) (wirelessDeviceProvisionStatus, error) {
						stamp, model := "12345", "SUCCESS"
						if revised && mode == "changed-stamp" {
							stamp = "12346"
						}
						if mode == "model-not-started" {
							model = "NOT_STARTED"
						}
						return wirelessDeviceProvisionStatus{deviceID: "wlc", name: "controller", siteID: "site", sitePath: "Global/Lab", networkIntent: "SUCCESS", modelConfig: model, lastProvisioned: stamp}, nil
					},
					func(selected map[string]string) (map[string]string, error) {
						if len(selected) != 1 || selected["profile:Wireless"] != "profile" {
							t.Fatal("missing selected profile")
						}
						if mode == "profile-error" {
							return nil, errors.New("dependency unavailable")
						}
						value := "original"
						if revised && mode == "changed-profile" {
							value = "changed"
						}
						return map[string]string{"profile-content:Wireless": value}, nil
					}, nil)
			}
			before, err := collect()
			invalid := mode == "missing-edge" || mode == "foreign-member" || mode == "replaced-site" || mode == "retained-unknown-vlan" || mode == "retained-unknown-ssid" || mode == "profile-error" || mode == "model-not-started"
			if invalid {
				if err == nil || before != nil {
					t.Fatal("incomplete prerequisites accepted", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(before.physical) != 2 || len(before.members) != 2 || len(before.profiles) != 2 || len(before.controllers) != 1 || len(before.payload) != 1 || len(before.payload[0].SSIDs) != 2 || len(before.digest) != 64 {
				t.Fatal("incomplete evidence", before)
			}
			if before.payload[0].SSIDs[0].Name != "Guest" || *before.payload[0].SSIDs[0].SecurityGroup != "Guests" {
				t.Fatal("retained policy lost")
			}
			revised = true
			if mode == "changed-tag" {
				tag = "Changed"
			}
			if mode == "changed-edge" {
				snapshot.devices[1].Serial = "FOC99999999"
			}
			after, err := collect()
			if err != nil {
				t.Fatal(err)
			}
			if (before.digest == after.digest) != (mode == "ready") {
				t.Fatal("dependency change lost")
			}
		})
	}
}
