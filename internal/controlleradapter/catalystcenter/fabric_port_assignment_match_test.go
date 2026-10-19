// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestFabricPortAssignmentMatches(t *testing.T) {
	port, value, empty, other := "GigabitEthernet1/0/1", "configured", "", "different"
	want := fabricPortAssignmentIntent{Interface: &port, Type: "USER_DEVICE"}
	row := fabricPortAssignment{Interface: port, Type: "USER_DEVICE"}
	if !fabricPortAssignmentMatches(want, row) {
		t.Fatal("minimal matching intent rejected")
	}
	for _, field := range []string{"data", "voice", "security", "authentication", "description"} {
		t.Run(field, func(t *testing.T) {
			w, r := want, row
			var desired, actual **string
			switch field {
			case "data":
				desired, actual = &w.DataVLAN, &r.DataVLAN
			case "voice":
				desired, actual = &w.VoiceVLAN, &r.VoiceVLAN
			case "security":
				desired, actual = &w.SecurityGroup, &r.SecurityGroup
			case "authentication":
				desired, actual = &w.Authentication, &r.Authentication
			case "description":
				desired, actual = &w.Description, &r.Description
			}
			*actual = &value
			if !fabricPortAssignmentMatches(w, r) {
				t.Fatal("omitted desired value imposed a default")
			}
			*desired = &value
			if !fabricPortAssignmentMatches(w, r) {
				t.Fatal("explicit matching value rejected")
			}
			*actual = nil
			if fabricPortAssignmentMatches(w, r) {
				t.Fatal("missing readback accepted")
			}
			*actual = &other
			if fabricPortAssignmentMatches(w, r) {
				t.Fatal("different value accepted")
			}
			*desired = &empty
			*actual = nil
			if fabricPortAssignmentMatches(w, r) {
				t.Fatal("absent readback equated to explicit empty")
			}
			*actual = &empty
			if !fabricPortAssignmentMatches(w, r) {
				t.Fatal("explicit empty readback rejected")
			}
		})
	}
	row.Interface = "gigabitethernet1/0/1"
	if fabricPortAssignmentMatches(want, row) {
		t.Fatal("non-exact physical interface accepted")
	}
	row.Interface = port
	row.Type = "ACCESS_POINT"
	if fabricPortAssignmentMatches(want, row) {
		t.Fatal("different device type accepted")
	}
	row.Type = want.Type
	want.Range = &other
	if fabricPortAssignmentMatches(want, row) {
		t.Fatal("unexpanded range accepted")
	}
}
