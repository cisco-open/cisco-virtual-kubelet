// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"
)

func TestFabricEmbeddedMulticastDependencies(t *testing.T) {
	for _, mode := range []string{"valid", "disabled", "unprovisioned", "unassigned", "missing-membership", "wrong-role", "missing-placement", "unknown-settings", "changed-settings"} {
		t.Run(mode, func(t *testing.T) {
			var revised atomic.Bool
			c, _ := ewlcComposedFixture(t, mode, func(w http.ResponseWriter, r *http.Request) bool {
				reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v}) }
				switch r.URL.Path {
				case "/dna/intent/api/v1/sda/fabrics/fabric/wirelessMulticast":
					reply(map[string]bool{"multicastEnabled": false})
					return true
				case "/dna/intent/api/v1/sda/fabrics/fabric/vlanToSsids":
					reply([]fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}}})
					return true
				case "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting":
					if mode == "unknown-settings" {
						w.WriteHeader(404)
						return true
					}
					percent := 25
					if mode == "changed-settings" && revised.Load() {
						percent = 15
					}
					reply([]fabricEWLCSetting{{ID: "edge-id", Enabled: mode != "disabled", Rolling: fabricEWLCRolling{Percentage: percent}}})
					return true
				}
				return false
			})
			ctx := context.Background()
			before, err := c.wirelessMulticastDependencies(ctx, "Global/Lab")
			valid := mode == "valid" || mode == "changed-settings" || mode == "missing-placement"
			// Omitted AP intent retains observed placement, including an empty set.
			if !valid {
				if err == nil || before != nil {
					t.Fatal("incomplete embedded evidence accepted", before, err)
				}
				return
			}
			if err != nil || len(before.embedded) != 1 || before.embedded[0].EmbeddedDigest == "" || len(before.members) != 2 {
				t.Fatal("missing dependency evidence", before, err)
			}
			priorResource := fabricWirelessMulticastResource("Global/Lab", true, before)
			guardCalls := 0
			write, err := c.planGuardedFabricWirelessMulticast(ctx, "Global/Lab", true, func(context.Context) error { guardCalls++; return errConfigurationDeviceAdmission })
			if err != nil || write == nil {
				t.Fatal("plan", err)
			}
			revised.Store(true)
			after, err := c.wirelessMulticastDependencies(ctx, "Global/Lab")
			if err != nil {
				t.Fatal(err)
			}
			if (before.digest == after.digest) != (mode != "changed-settings") || (priorResource == fabricWirelessMulticastResource("Global/Lab", true, after)) != (mode != "changed-settings") {
				t.Fatal("embedded settings omitted from durable evidence")
			}
			_, err = write.submit(ctx)
			if mode == "changed-settings" {
				if !errors.Is(err, errConfigurationStale) || guardCalls != 0 {
					t.Fatal("changed settings reached mutation authority", err)
				}
			} else if !errors.Is(err, errConfigurationDeviceAdmission) || guardCalls != 1 {
				t.Fatal("native plan bypassed authority", err)
			}
		})
	}
}
