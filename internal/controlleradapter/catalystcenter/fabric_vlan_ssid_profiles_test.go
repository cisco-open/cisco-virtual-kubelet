// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"errors"
	"testing"
)

func TestFabricVLANSSIDProfiles(t *testing.T) {
	for _, mode := range []string{"direct", "descendant", "unassigned", "outside", "nonfabric", "unknown-flag", "flex", "ambiguous", "missing-definition", "sensor", "late-error", "unknown-profile", "missing-root"} {
		t.Run(mode, func(t *testing.T) {
			fields := map[string]any{"ssidName": "Staff", "enableFabric": true}
			profile := wirelessProfileMetadata{id: "profile", name: "Wireless", fields: map[string]any{"ssidDetails": []map[string]any{fields}}}
			snapshot := wirelessInventoryDependencies{sites: map[string]Site{"Global/Lab": {ID: "site", NameHierarchy: "Global/Lab"}, "Global/Lab/Building": {ID: "building", NameHierarchy: "Global/Lab/Building"}, "Global/Laboratory": {ID: "other", NameHierarchy: "Global/Laboratory"}}, profiles: map[string]wirelessProfileMetadata{"Wireless": profile}, profileSites: map[string]map[string]bool{"profile": {"site": true}}}
			switch mode {
			case "descendant":
				snapshot.profileSites["profile"] = map[string]bool{"building": true}
			case "unassigned":
				snapshot.profileSites["profile"] = nil
			case "outside":
				snapshot.profileSites["profile"] = map[string]bool{"other": true}
			case "nonfabric":
				fields["enableFabric"] = false
			case "unknown-flag":
				delete(fields, "enableFabric")
			case "flex":
				fields["flexConnect"] = map[string]any{"enableFlexConnect": true}
			case "ambiguous":
				other := profile
				other.id = "second"
				snapshot.profiles["Other"] = other
				snapshot.profileSites["second"] = map[string]bool{"site": true}
			case "unknown-profile":
				profile.unmapped = true
				snapshot.profiles["Wireless"] = profile
			case "missing-root":
				delete(snapshot.sites, "Global/Lab")
			}
			reads := 0
			got, err := resolveFabricSSIDProfiles("Global/Lab", map[string]bool{"Staff": true}, snapshot, func(id string) (map[string]wirelessSSIDMetadata, error) {
				reads++
				if mode == "late-error" {
					return nil, errors.New("unavailable")
				}
				if mode == "missing-definition" {
					return map[string]wirelessSSIDMetadata{}, nil
				}
				return map[string]wirelessSSIDMetadata{"Staff": {id: "ssid", name: "Staff", sensor: mode == "sensor", inheritedSiteID: "global"}}, nil
			})
			valid := mode == "direct" || mode == "descendant"
			if (err == nil) != valid {
				t.Fatal("profile eligibility", err)
			}
			if !valid {
				if got != nil {
					t.Fatal("partial evidence")
				}
				return
			}
			if len(got) != 1 || reads != 1 || got[0].ProfileID != "profile" || got[0].SSIDID != "ssid" || got[0].InheritedSiteID != "global" {
				t.Fatal("incomplete identity evidence", got, reads)
			}
			if mode == "descendant" && got[0].SiteID != "building" {
				t.Fatal("wrong assignment")
			}
		})
	}
}
