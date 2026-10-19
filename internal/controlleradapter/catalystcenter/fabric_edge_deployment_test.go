// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"strings"
	"testing"
)

func TestFabricEdgeDeploymentModel(t *testing.T) {
	for _, mode := range []string{"channel", "assignment", "mixed", "overlap", "bad-channel", "bad-assignment", "unknown-device", "unconsumed-section"} {
		t.Run(mode, func(t *testing.T) {
			raw := string(portChannelTestModel("[" + portChannelTestRow + "]"))
			if mode == "assignment" {
				raw = string(portAssignmentTestModel("[" + portAssignmentTestRow + "]"))
			}
			if mode != "channel" && mode != "assignment" {
				row := strings.Replace(portAssignmentTestRow, "GigabitEthernet1/0/1", "GigabitEthernet1/0/3", 1)
				if mode == "overlap" {
					row = strings.Replace(row, "GigabitEthernet1/0/3", "gigabitethernet1/0/1", 1)
				}
				if mode == "bad-assignment" {
					row = strings.Replace(row, "USER_DEVICE", "UNKNOWN", 1)
				}
				raw = strings.Replace(raw, `"port_channels":`, `"port_assignments":[`+row+`],"port_channels":`, 1)
				if mode == "bad-channel" {
					raw = strings.Replace(raw, `"id":7`, `"id":7,"unrecognized":true`, 1)
				}
				if mode == "unknown-device" {
					raw = strings.Replace(raw, `"name":"edge"`, `"name":"edge","other_effect":true`, 1)
				}
				if mode == "unconsumed-section" {
					raw = strings.Replace(raw, `"inventory":`, `"templates":{},"inventory":`, 1)
				}
			}
			got, err := parseFabricEdgeDeployment([]byte(raw))
			valid := mode == "channel" || mode == "assignment" || mode == "mixed"
			if (err == nil) != valid {
				t.Fatal("model acceptance", err)
			}
			if !valid {
				if got != nil {
					t.Fatal("partial model returned")
				}
				return
			}
			if len(got.devices) != 1 {
				t.Fatal("base declarations lost")
			}
			if (len(got.channels) == 1) != (mode != "assignment") || (len(got.assignments) == 1) != (mode != "channel") {
				t.Fatal("extension lost")
			}
			if mode == "mixed" {
				if _, _, err := parseFabricPortChannelDeployment(got.channelSource); err != nil {
					t.Fatal(err)
				}
				if _, _, err := parseFabricPortAssignmentDeployment(got.assignmentSource); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}
