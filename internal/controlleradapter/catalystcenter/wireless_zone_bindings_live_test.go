//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"os"
	"reflect"
	"testing"
)

func TestLiveWirelessZoneIdentityBindings(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_ZONE_BINDINGS") != "isolated-definitions" {
		t.Skip("explicit isolated zone-identity qualification disabled")
	}
	for _, key := range []string{"CATC_WIRELESS_PROFILE_RECOVERY", "CATC_POLICY_COHORT_EXTENSION_PROBE", "CATC_WIRELESS_VLAN_SELECTOR"} {
		if value := os.Getenv(key); value != "" && value != "disabled" {
			t.Fatal("zone identity qualification requires separate definition-only fixture mode")
		}
	}
	// Reuse the existing disabled-SSID/profile definitions and exact cleanup;
	// no area, tag, site assignment or physical object is created by this mode.
	qualifyLiveWirelessProfileExecution(t, false, true, "")
}

func assertLiveWirelessZoneBindings(t *testing.T, ctx context.Context, c *client, name string, expected wirelessZoneBindings) {
	t.Helper()
	for range 2 {
		profiles, err := c.wirelessProfileInventory(ctx)
		if err != nil || profiles[name].id != expected.ParentID {
			t.Fatal("native zone parent identity changed", err)
		}
		profile := profiles[name]
		all, err := c.wirelessProfileZoneBindings(ctx, profile, nil)
		if err != nil || !reflect.DeepEqual(all, expected) {
			t.Fatal("complete native zone RF/SSID identity snapshot differs", err)
		}
		for zone, binding := range expected.Zones {
			selected, err := c.wirelessProfileZoneBindings(ctx, profile, []string{zone})
			if err != nil || len(selected.Zones) != 1 || !reflect.DeepEqual(selected.Zones[zone], binding) || len(selected.RFIDs) != 1 || selected.RFIDs[binding.RF] != expected.RFIDs[binding.RF] || !reflect.DeepEqual(selected.SSIDIDs, expected.SSIDIDs) {
				t.Fatal("selected native zone widened or lost its dependency UUIDs", err)
			}
		}
	}
	t.Log("two complete-parent and per-zone native identity snapshots matched original RF/SSID UUIDs; collector performs no mutation; receipt-version integration remains separate")
}
