// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"testing"
)

func TestFabricVLANSSIDControllers(t *testing.T) {
	for _, mode := range []string{"ready", "no-model", "required-model", "no-wlc", "unreachable", "wrong-family", "missing-inventory", "duplicate-serial", "replaced", "wrong-site", "out-of-sync", "running", "failed", "no-stamp", "bad-stamp", "read-error", "second-failed"} {
		t.Run(mode, func(t *testing.T) {
			members := map[string]fabricDeviceMembership{"wlc": {ID: "member", NetworkDeviceID: "wlc", FabricID: "fabric", Roles: []string{"WIRELESS_CONTROLLER_NODE"}}}
			inventory := []Device{{ID: "wlc", Serial: "FOC12345678", Hostname: "controller", Family: "Wireless Controller", Reachability: "Reachable"}}
			sites := map[string]Site{"Global/DC": {ID: "site", NameHierarchy: "Global/DC"}}
			current := wirelessDeviceProvisionStatus{deviceID: "wlc", name: "controller", siteID: "site", sitePath: "Global/DC", networkIntent: "SUCCESS", modelConfig: "SUCCESS", lastProvisioned: "12345"}
			switch mode {
			case "no-model", "required-model":
				current.modelConfig = "NOT_STARTED"
			case "no-wlc":
				members = map[string]fabricDeviceMembership{}
			case "unreachable":
				inventory[0].Reachability = "Unreachable"
			case "wrong-family":
				inventory[0].Family = "Switches and Hubs"
			case "missing-inventory":
				inventory = nil
			case "duplicate-serial":
				copy := inventory[0]
				copy.ID = "other"
				inventory = append(inventory, copy)
			case "replaced":
				current.deviceID = "replacement"
			case "wrong-site":
				current.siteID = "replacement"
			case "out-of-sync":
				current.outOfSync = true
			case "running":
				current.networkIntent = "CONFIGURING"
			case "failed":
				current.modelConfig = "FAILED"
			case "no-stamp":
				current.lastProvisioned = ""
			case "bad-stamp":
				current.lastProvisioned = "001"
			case "second-failed":
				members["wlc-two"] = fabricDeviceMembership{ID: "member-two", NetworkDeviceID: "wlc-two", FabricID: "fabric", Roles: []string{"WIRELESS_CONTROLLER_NODE"}}
				copy := inventory[0]
				copy.ID = "wlc-two"
				copy.Serial = "FOC87654321"
				inventory = append(inventory, copy)
			}
			got, err := resolveFabricSSIDControllers("fabric", members, inventory, sites, mode == "required-model", func(id string) (wirelessDeviceProvisionStatus, error) {
				if mode == "read-error" {
					return wirelessDeviceProvisionStatus{}, errors.New("unavailable")
				}
				copy := current
				if id == "wlc-two" {
					copy.deviceID = id
					copy.networkIntent = "FAILED"
				}
				return copy, nil
			})
			valid := mode == "ready" || mode == "no-model"
			if (err == nil) != valid {
				t.Fatal("readiness", err)
			}
			if !valid {
				if got != nil {
					t.Fatal("partial controller scope")
				}
				return
			}
			if len(got) != 1 || got[0].Device.Serial != "foc12345678" || got[0].Membership.ID != "member" || got[0].LastProvisioned != "12345" {
				t.Fatal("evidence lost", got)
			}
		})
	}
}
