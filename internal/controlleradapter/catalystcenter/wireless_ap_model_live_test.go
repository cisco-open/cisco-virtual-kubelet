// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"testing"
	"time"
)

func TestLiveWirelessAPMetadata(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	inventory, err := liveClient(t).wirelessAPMetadataInventory(ctx)
	if err != nil {
		t.Fatal("AP metadata read failed", err)
	}
	for _, p := range inventory {
		for _, path := range wirelessAPPasswords {
			if _, ok := getWirelessAPField(p.fields, path); ok {
				t.Fatal("credential retained")
			}
		}
	}
	t.Log("AP metadata inventory count", len(inventory), "no configuration submitted")
}
