// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"testing"
	"time"
)

func TestLiveCatalystCenterConfigurationIdentities(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, ip := range []string{"198.51.100.100", "198.51.100.101", "198.51.100.103"} {
		selected, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil {
			t.Fatal("lab device identity unavailable", err)
		}
		if selected.Serial == "" {
			t.Fatal("lab physical identity missing")
		}
		corroborated, err := resolveConfigurationDevice(configurationDeviceReference{Name: selected.Hostname, IP: ip, Serial: selected.Serial}, inventory)
		if err != nil || corroborated.ID != selected.ID {
			t.Fatal("lab selectors do not agree", err)
		}
		checked++
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	assignments := map[string]bool{}
	for _, site := range sites {
		members, err := c.assignedDevices(ctx, site.ID)
		if err != nil {
			t.Fatal("assignment read failed", err)
		}
		for id := range members {
			if assignments[id] {
				t.Fatal("device assigned to multiple sites")
			}
			assignments[id] = true
		}
	}
	t.Logf("verified assignment reads across %d sites; assigned devices=%d", len(sites), len(assignments))
	t.Logf("configuration inventory entries=%d; verified lab device bindings=%d; no writes or device values printed", len(inventory), checked)
}
