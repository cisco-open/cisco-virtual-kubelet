// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func TestFabricDeviceMembershipInventory(t *testing.T) {
	for _, mode := range []string{"paged", "empty", "wrong-fabric", "duplicate-device", "duplicate-id", "unknown-role", "duplicate-role", "missing-roles", "missing-device", "late-error"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != fabricDevicesPath || r.URL.Query().Get("fabricId") != "fabric" || r.URL.Query().Get("limit") != "500" {
					t.Error("incorrect membership request")
					w.WriteHeader(400)
					return
				}
				reads++
				rows := []fabricDeviceMembership{}
				if mode != "empty" {
					if reads == 1 {
						for i := 0; i < 500; i++ {
							rows = append(rows, fabricDeviceMembership{ID: fmt.Sprintf("membership-%d", i), NetworkDeviceID: fmt.Sprintf("device-%d", i), FabricID: "fabric", Roles: []string{"CONTROL_PLANE_NODE", "BORDER_NODE"}})
						}
					} else {
						if r.URL.Query().Get("offset") != "501" {
							t.Error("pagination offset")
						}
						if mode == "late-error" {
							w.WriteHeader(500)
							return
						}
						row := fabricDeviceMembership{ID: "membership-last", NetworkDeviceID: "device-last", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}
						switch mode {
						case "wrong-fabric":
							row.FabricID = "other"
						case "duplicate-device":
							row.NetworkDeviceID = "device-0"
						case "duplicate-id":
							row.ID = "membership-0"
						case "unknown-role":
							row.Roles = []string{"NEW_ROLE"}
						case "duplicate-role":
							row.Roles = []string{"EDGE_NODE", "EDGE_NODE"}
						case "missing-roles":
							row.Roles = nil
						case "missing-device":
							row.NetworkDeviceID = ""
						}
						rows = append(rows, row)
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			got, err := c.fabricDeviceMemberships(context.Background(), "fabric")
			if mode == "paged" {
				if err != nil || len(got) != 501 || reads != 2 || !reflect.DeepEqual(got["device-0"].Roles, []string{"BORDER_NODE", "CONTROL_PLANE_NODE"}) {
					t.Fatal("membership paging", err, len(got), reads)
				}
				return
			}
			if mode == "empty" {
				if err != nil || len(got) != 0 {
					t.Fatal("empty", err)
				}
				return
			}
			if err == nil || got != nil {
				t.Fatal("partial invalid membership inventory", err)
			}
		})
	}
}
func TestFabricMulticastRPRoleConstraints(t *testing.T) {
	for _, mode := range []string{"border", "edge", "two-borders", "border-edge", "two-edges", "three-borders", "dual-one", "dual-two", "controller-only", "missing-member", "wrong-fabric", "wrong-device", "duplicate-reference", "none"} {
		t.Run(mode, func(t *testing.T) {
			members := map[string]fabricDeviceMembership{}
			for _, id := range []string{"a", "b", "c"} {
				members[id] = fabricDeviceMembership{ID: "membership-" + id, NetworkDeviceID: id, FabricID: "fabric", Roles: []string{"BORDER_NODE"}}
			}
			ids := []string{"a"}
			dual := false
			edge := func(id string) { row := members[id]; row.Roles = []string{"EDGE_NODE"}; members[id] = row }
			switch mode {
			case "edge":
				edge("a")
			case "two-borders":
				ids = []string{"a", "b"}
			case "border-edge":
				ids = []string{"a", "b"}
				edge("b")
			case "two-edges":
				ids = []string{"a", "b"}
				edge("a")
				edge("b")
			case "three-borders":
				ids = []string{"a", "b", "c"}
			case "dual-one":
				dual = true
			case "dual-two":
				dual = true
				ids = []string{"a", "b"}
			case "controller-only":
				row := members["a"]
				row.Roles = []string{"CONTROL_PLANE_NODE"}
				members["a"] = row
			case "missing-member":
				delete(members, "a")
			case "wrong-fabric":
				row := members["a"]
				row.FabricID = "other"
				members["a"] = row
			case "wrong-device":
				row := members["a"]
				row.NetworkDeviceID = "other"
				members["a"] = row
			case "duplicate-reference":
				ids = []string{"a", "a"}
			case "none":
				ids = nil
			}
			err := validateFabricMulticastRPSet("fabric", ids, members, dual)
			valid := mode == "border" || mode == "edge" || mode == "two-borders" || mode == "dual-one" || mode == "duplicate-reference" || mode == "none"
			if (err == nil) != valid {
				t.Fatal("incorrect role constraint", err)
			}
		})
	}
}
