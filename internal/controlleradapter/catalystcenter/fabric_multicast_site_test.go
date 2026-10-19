// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func multicastSiteRow(t *testing.T, name, pool string, ids ...string) fabricMulticastVN {
	t.Helper()
	devices := []any{}
	for _, id := range ids {
		devices = append(devices, id)
	}
	rps := []any{}
	if len(ids) > 0 {
		rps = append(rps, map[string]any{"rpDeviceLocation": "FABRIC", "networkDeviceIds": devices, "ipv4Address": "192.0.2.1"})
	}
	raw, _ := json.Marshal(map[string]any{"id": "multicast-" + name, "fabricId": "fabric", "virtualNetworkName": name, "ipPoolName": pool, "multicastRPs": rps, "ipv4SsmRanges": []any{}})
	row, err := parseFabricMulticastVNObservation(raw)
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func TestFabricMulticastSiteUnion(t *testing.T) {
	for _, mode := range []string{"two-vns", "shared-rp", "third-rp", "edge-other-vn", "dual-other-vn", "dual-no-rps", "foreign-fabric", "missing-member", "missing-pool", "changed-target-id", "duplicate-proposal", "unknown-retained", "missing-retained-rps", "empty-site"} {
		t.Run(mode, func(t *testing.T) {
			pool := fixturePools()[0]
			reservation := fixtureReservations()[0]
			pools := &anycastPoolInventory{byID: map[string]ipPool{pool.ID: pool}, byName: map[string]ipReservation{reservation.Name: reservation}}
			other := reservation
			other.ID = "other-reservation"
			other.Name = "other-pool"
			if mode == "dual-other-vn" || mode == "dual-no-rps" {
				bits := 64
				globalBits := 48
				other.V6 = &poolAddressSpace{Subnet: "2001:db8:1::", PrefixLength: &bits, GlobalPoolID: "global-v6"}
				pools.byID["global-v6"] = ipPool{ID: "global-v6", Name: "global-v6", Type: "Generic", Space: &poolAddressSpace{Subnet: "2001:db8:1::", PrefixLength: &globalBits}}
			}
			pools.byName[other.Name] = other
			retained := multicastSiteRow(t, "Other", "other-pool", "b")
			wanted := multicastSiteRow(t, "Corp", "access", "a")
			current := map[fabricMulticastVNKey]fabricMulticastVN{{"fabric", "Other"}: retained}
			proposed := []fabricMulticastVN{wanted}
			members := map[string]fabricDeviceMembership{}
			for _, id := range []string{"a", "b", "c"} {
				members[id] = fabricDeviceMembership{ID: "member-" + id, NetworkDeviceID: id, FabricID: "fabric", Roles: []string{"BORDER_NODE"}}
			}
			switch mode {
			case "shared-rp":
				proposed = []fabricMulticastVN{multicastSiteRow(t, "Corp", "access", "b")}
			case "third-rp":
				proposed = []fabricMulticastVN{multicastSiteRow(t, "Corp", "access", "a", "c")}
			case "edge-other-vn":
				row := members["b"]
				row.Roles = []string{"EDGE_NODE"}
				members["b"] = row
			case "dual-no-rps":
				current[fabricMulticastVNKey{"fabric", "Other"}] = multicastSiteRow(t, "Other", "other-pool")
			case "foreign-fabric":
				foreign := multicastSiteRow(t, "Foreign", "absent-pool", "c")
				foreign.FabricID = "elsewhere"
				foreign.fields["fabricId"] = "elsewhere"
				current[fabricMulticastVNKey{"elsewhere", "Foreign"}] = foreign
			case "missing-member":
				delete(members, "b")
			case "missing-pool":
				delete(pools.byName, "other-pool")
			case "changed-target-id":
				before := wanted
				before.ID = "original"
				current[fabricMulticastVNKey{"fabric", "Corp"}] = before
			case "duplicate-proposal":
				proposed = append(proposed, wanted)
			case "unknown-retained":
				retained.unknownFields = true
				current[fabricMulticastVNKey{"fabric", "Other"}] = retained
			case "missing-retained-rps":
				delete(retained.fields, "multicastRPs")
			case "empty-site":
				current = map[fabricMulticastVNKey]fabricMulticastVN{}
				proposed = nil
			}
			result, err := validateFabricMulticastSite("fabric", "area", current, proposed, pools, members)
			valid := mode == "two-vns" || mode == "shared-rp" || mode == "dual-no-rps" || mode == "foreign-fabric" || mode == "empty-site"
			if (err == nil) != valid {
				t.Fatal("incorrect cross-VN validation", err)
			}
			if !valid {
				if !reflect.DeepEqual(result, fabricMulticastSiteEvidence{}) {
					t.Fatal("partial evidence")
				}
				return
			}
			expected := 2
			if mode == "shared-rp" || mode == "dual-no-rps" {
				expected = 1
			}
			if mode == "empty-site" {
				expected = 0
			}
			if len(result.members) != expected {
				t.Fatal("wrong union", len(result.members))
			}
			if mode != "empty-site" && (result.poolDigests["Corp"] == "" || result.poolDigests["Other"] == "") {
				t.Fatal("unmanaged VN pool not bound")
			}
		})
	}
}
