// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"os"
	"reflect"
	"testing"
	"time"
)

// Read-only qualification. This test has no path that submits START, reset,
// stop or another native mutation, even when a declared target is supplied.
func TestLiveLANSessionLaunchPrerequisites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	active, err := c.lanActiveSessions(ctx)
	if err != nil {
		t.Fatal("native active-session read", err)
	}
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal("native seed inventory read", err)
	}
	pnp, err := c.pnpDevices(ctx)
	if err != nil {
		t.Fatal("native PnP inventory read", err)
	}
	pools, err := c.loadAnycastPoolInventory(ctx)
	if err != nil {
		t.Fatal("native IPAM dependency read", err)
	}
	lan, usageAvailable := 0, 0
	for _, p := range pools.byName {
		if p.Type == "LAN" && p.V4 != nil {
			lan++
			if _, err := c.lanPoolUsage(ctx, p); err == nil {
				usageAvailable++
			} else {
				t.Log("one LAN pool has unavailable/invalid usage; not qualified as launch capacity")
			}
		}
	}
	after, err := c.lanActiveSessions(ctx)
	if err != nil || !reflect.DeepEqual(active, after) {
		t.Fatal("active-session snapshot changed", err)
	}
	t.Logf("read-only prerequisites: maximum=%d active=%d managed=%d PnP=%d LAN-pools=%d complete-usage=%d; no launch/reset/stop submitted", active.Maximum, len(active.Active), len(devices), len(pnp), lan, usageAvailable)
	t.Run("DeclaredLaunch", func(t *testing.T) {
		path := os.Getenv("CATC_LAN_SESSION_MODEL")
		if path == "" {
			t.Skip("no explicit isolated LAN session declaration supplied; full native launch preflight unqualified")
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal("declared model unavailable")
		}
		wants, _, err := parseLANSessionIntents(raw, false)
		if err != nil {
			t.Fatal("declared metadata invalid", err)
		}
		for _, want := range wants {
			before, err := c.lanSessionPreflight(ctx, want)
			if err != nil {
				t.Fatal("declared launch not ready", err)
			}
			after, err := c.lanSessionPreflight(ctx, want)
			if err != nil || before.observationDigest() != after.observationDigest() {
				t.Fatal("launch preflight changed", err)
			}
		}
		t.Log("declared preflight observations stable; this is not physical admission or native START qualification")
	})
}
