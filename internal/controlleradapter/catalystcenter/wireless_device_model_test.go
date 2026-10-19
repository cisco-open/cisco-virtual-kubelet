// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "testing"

func TestWirelessDevicePlacementModel(t *testing.T) {
	for _, scenario := range []string{"standalone", "reprovision", "anchor", "fabric", "embedded", "explicit-empty"} {
		fields := map[string]any{"state": "PROVISION", "type": "WirelessController", "primary_managed_ap_locations": []any{"Global/B", "Global/A"}}
		switch scenario {
		case "reprovision":
			fields["state"] = "REPROVISION"
		case "anchor":
			delete(fields, "primary_managed_ap_locations")
			fields["anchor_managed_ap_locations"] = []any{"Global/A"}
		case "fabric":
			fields["fabric_roles"] = []any{"WIRELESS_CONTROLLER_NODE"}
		case "embedded":
			delete(fields, "type")
			fields["fabric_roles"] = []any{"EMBEDDED_WIRELESS_CONTROLLER_NODE"}
		case "explicit-empty":
			fields["primary_managed_ap_locations"] = []any{}
		}
		got, err := parseWirelessDevicePlacement(fields)
		if err != nil {
			t.Fatal(scenario, err)
		}
		if scenario == "explicit-empty" && (got.locations.primary == nil || got.locations.secondary != nil) {
			t.Fatal("lost absent/empty distinction")
		}
		if scenario == "standalone" && got.locations.primary[0] != "Global/A" {
			t.Fatal("unsorted sites")
		}
	}
}
func TestWirelessDevicePlacementRejectsContradictions(t *testing.T) {
	for _, scenario := range []string{"missing-type", "no-sites", "both-roles", "wrong-role", "anchor-fabric", "anchor-fabric-site", "null", "duplicate", "global", "bad-state", "unknown"} {
		fields := map[string]any{"state": "PROVISION", "type": "WirelessController", "primary_managed_ap_locations": []any{"Global/A"}}
		switch scenario {
		case "missing-type":
			delete(fields, "type")
		case "no-sites":
			delete(fields, "primary_managed_ap_locations")
		case "both-roles":
			fields["fabric_roles"] = []any{"WIRELESS_CONTROLLER_NODE", "EMBEDDED_WIRELESS_CONTROLLER_NODE"}
		case "wrong-role":
			fields["fabric_roles"] = []any{"EDGE_NODE"}
		case "anchor-fabric":
			fields["fabric_roles"] = []any{"WIRELESS_CONTROLLER_NODE"}
			fields["anchor_managed_ap_locations"] = []any{"Global/A"}
		case "anchor-fabric-site":
			fields["fabric_site"] = "Fabric"
			fields["anchor_managed_ap_locations"] = []any{"Global/A"}
		case "null":
			fields["primary_managed_ap_locations"] = nil
		case "duplicate":
			fields["primary_managed_ap_locations"] = []any{"Global/A", "Global/A"}
		case "global":
			fields["primary_managed_ap_locations"] = []any{"Global"}
		case "bad-state":
			fields["state"] = "UNPROVISION"
		case "unknown":
			fields["credential"] = "ignored?"
		}
		if _, err := parseWirelessDevicePlacement(fields); err == nil {
			t.Fatal("accepted invalid model", scenario)
		}
	}
}
