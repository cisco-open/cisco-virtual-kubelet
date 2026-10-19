// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"testing"
	"time"
)

// Read native fabric state without requesting mutation authority. Empty lab
// topology is not evidence that multicast or embedded WLC is qualified.
func TestLiveCatalystCenterFabricInventoryRead(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sites, err := c.fabricSites(ctx)
	if err != nil {
		t.Fatal("fabric site inventory", err)
	}
	if len(sites) == 0 {
		t.Skip("no fabric sites: multicast and embedded WLC qualification unavailable")
	}
	fabrics := map[string]bool{}
	for _, site := range sites {
		fabrics[site.ID] = true
	}
	t.Run("Replication", func(t *testing.T) {
		rows, err := c.fabricMulticastReplicationModes(ctx)
		if err != nil {
			t.Fatal("replication inventory", err)
		}
		for id := range rows {
			if !fabrics[id] {
				t.Fatal("replication references a fabric absent from site inventory")
			}
		}
		if len(rows) == 0 {
			t.Skip("no configured replication modes to qualify")
		}
		t.Logf("validated %d native replication rows; no writes", len(rows))
	})
	t.Run("MulticastVirtualNetworks", func(t *testing.T) {
		rows, err := c.fabricMulticastVNs(ctx)
		if err != nil {
			t.Fatal("multicast virtual network inventory", err)
		}
		for _, row := range rows {
			if !fabrics[row.FabricID] {
				t.Fatal("multicast VN references a fabric absent from site inventory")
			}
		}
		if len(rows) == 0 {
			t.Skip("no configured multicast VNs to qualify")
		}
		t.Logf("validated %d native multicast VN rows; no writes", len(rows))
	})
	t.Run("EmbeddedWirelessControllers", func(t *testing.T) {
		count := 0
		for id := range fabrics {
			rows, err := c.fabricEWLCSettings(ctx, id)
			if err != nil {
				t.Fatal("embedded wireless settings inventory", err)
			}
			count += len(rows)
		}
		if count == 0 {
			t.Skip("no embedded wireless settings to qualify")
		}
		t.Logf("validated %d native embedded wireless settings rows; no writes", count)
	})
}
