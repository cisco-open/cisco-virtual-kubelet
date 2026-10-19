// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "testing"

func TestFabricPortAssignmentSelection(t *testing.T) {
	for _, mode := range []string{"missing", "existing", "partial", "conflict", "case-conflict", "duplicate-id", "duplicate-port", "unrelated"} {
		t.Run(mode, func(t *testing.T) {
			rangeValue := "GigabitEthernet1/0/1-1/0/2"
			wants := []fabricPortAssignmentIntent{{Range: &rangeValue, Type: "USER_DEVICE"}}
			before := []fabricPortAssignment{{ID: "first", Interface: "GigabitEthernet1/0/1", Type: "USER_DEVICE"}, {ID: "second", Interface: "GigabitEthernet1/0/2", Type: "USER_DEVICE"}}
			switch mode {
			case "missing":
				before = nil
			case "partial":
				before = before[:1]
			case "conflict":
				before[0].Type = "ACCESS_POINT"
			case "case-conflict":
				before[0].Interface = "gigabitethernet1/0/1"
			case "duplicate-id":
				before[1].ID = before[0].ID
			case "duplicate-port":
				before[1].Interface = before[0].Interface
			case "unrelated":
				before = append(before, fabricPortAssignment{ID: "other", Interface: "GigabitEthernet1/0/3", Type: "ACCESS_POINT"})
			}
			got, err := selectFabricPortAssignments(wants, before)
			valid := mode == "missing" || mode == "existing" || mode == "partial" || mode == "unrelated"
			if (err == nil) != valid {
				t.Fatal("selection outcome", err)
			}
			if !valid {
				if got != nil {
					t.Fatal("conflict returned executable selection")
				}
				return
			}
			missing := 0
			if mode == "missing" {
				missing = 2
			}
			if mode == "partial" {
				missing = 1
			}
			if len(got.missing) != missing || len(got.existing) != 2-missing {
				t.Fatal("incorrect selection")
			}
			if mode == "partial" && (*got.missing[0].Interface != "GigabitEthernet1/0/2" || got.existing[0].ID != "first") {
				t.Fatal("lost native identity")
			}
		})
	}
}
