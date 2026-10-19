// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "testing"

func TestFabricVLANSSIDSequentialSelection(t *testing.T) {
	tag := "Guests"
	before := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Guest", SecurityGroup: &tag}}}}
	want := []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff"}, {Name: "Engineering"}, {Name: "Guest"}}}}
	first, claim, err := selectNextFabricVLANSSID(want, before, nil)
	if err != nil || claim == nil || claim.SSIDs[0].Name != "Engineering" || len(first.payload[0].SSIDs) != 2 {
		t.Fatal("first selection", claim, err)
	}
	second, claim, err := selectNextFabricVLANSSID(want, first.payload, nil)
	if err != nil || claim == nil || claim.SSIDs[0].Name != "Staff" || len(second.payload[0].SSIDs) != 3 {
		t.Fatal("second selection", claim, err)
	}
	final, claim, err := selectNextFabricVLANSSID(want, second.payload, nil)
	if err != nil || claim != nil || len(final.added)+len(final.moved) != 0 {
		t.Fatal("adopted unchanged rows", claim, err)
	}
	for _, row := range final.payload {
		for _, ssid := range row.SSIDs {
			if ssid.Name == "Guest" && (ssid.SecurityGroup == nil || *ssid.SecurityGroup != "Guests") {
				t.Fatal("policy lost")
			}
		}
	}
	// A later unowned move must block the earlier addition before any write.
	want = append(want, fabricVLANSSID{VLAN: "Other", SSIDs: []fabricSSIDBinding{{Name: "Guest"}}})
	want[0].SSIDs = want[0].SSIDs[:2]
	if got, claim, err := selectNextFabricVLANSSID(want, before, nil); err == nil || got != nil || claim != nil {
		t.Fatal("late conflict hidden")
	}
}
