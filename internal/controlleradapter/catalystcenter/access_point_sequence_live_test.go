// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// Read-only qualification of the refusal boundary. An accidental plan is an
// error, and no submission closure is ever invoked, even on unexpected success.
func TestLiveAccessPointSequenceRefusesWiredInventory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := liveClient(t)
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := parseAccessPointProfiles([]any{map[string]any{"name": "CVK-negative-AP-selection", "led_status": false}})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, device := range inventory {
		if device.Family != "Switches and Hubs" || strings.HasPrefix(device.PlatformID, "C91") || strings.HasPrefix(device.PlatformID, "CW91") || device.Serial == "" {
			continue
		}
		want, err := parseAccessPointDevice(map[string]any{"name": device.Hostname, "serial_number": device.Serial, "type": "AccessPoint", "site": "Global/CVK-negative-AP-selection", "state": "PROVISION", "rf_profile": "TYPICAL", "access_point": map[string]any{"configuration": profiles[0].name}}, profiles)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := c.planAccessPointConfigurationSequence(ctx, []accessPointDeviceIntent{want}, func(context.Context, string, string, string, []accessPointConfigurationMember) error {
			t.Fatal("wired inventory requested AP mutation authority")
			return errConfigurationDeviceAdmission
		})
		if !errors.Is(err, errAccessPointIdentity) || plan != nil {
			t.Fatal("wired inventory was not rejected at AP identity resolution", err)
		}
		checked++
		if checked == 3 {
			break
		}
	}
	if checked == 0 {
		t.Skip("no wired switch identity available for negative AP selection qualification")
	}
	t.Logf("AP sequence rejected %d real wired identities before authority; no native writes or device operations", checked)
}
