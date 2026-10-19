// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"testing"
	"time"
)

func TestLiveWirelessSSIDInventory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	c := liveClient(t)
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, site := range sites {
		if site.NameHierarchy == "Global" && site.Type == "global" {
			if id != "" {
				t.Fatal("duplicate Global site")
			}
			id = site.ID
		}
	}
	if id == "" {
		t.Fatal("Global site absent")
	}
	rows, err := c.wirelessSSIDInventory(ctx, id)
	if err != nil {
		t.Fatal("SSID metadata read", err)
	}
	t.Log("SSID metadata inventory count", len(rows), "Global site", id, "no configuration submitted")
}
