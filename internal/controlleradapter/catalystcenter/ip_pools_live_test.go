// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

func TestLiveCatalystCenterIPAMReport(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	pools, err := c.listIPPools(ctx)
	if err != nil {
		t.Fatal(err)
	}
	reservations, err := c.listIPReservations(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sitePath := ""
	siteKind := ""
	for _, s := range sites {
		if s.Type != "global" {
			sitePath = s.NameHierarchy
			siteKind = s.Type
			break
		}
	}
	if sitePath == "" {
		t.Fatal("no non-global site for reference qualification")
	}
	name := "CVK-ReadOnly-IPAM-Qualification"
	reservationName := "CVK-ReadOnly-Reservation-Qualification"
	for _, p := range pools {
		if p.Name == name {
			t.Fatal("synthetic identity already exists")
		}
	}
	for _, r := range reservations {
		if r.Name == reservationName {
			t.Fatal("synthetic reservation already exists")
		}
	}
	var model map[string]any
	json.Unmarshal([]byte(poolModel), &model)
	root := model["catalyst_center"].(map[string]any)
	root["sites"] = map[string]any{siteKind + "s": []any{map[string]any{"name": lastSiteName(sitePath), "parent_name": parentSitePath(sitePath), "ip_pools_reservations": []string{reservationName}}}}
	pool := root["network_settings"].(map[string]any)["ip_pools"].([]any)[0].(map[string]any)
	pool["name"] = name
	pool["ip_pools_reservations"].([]any)[0].(map[string]any)["name"] = reservationName
	raw, _ := json.Marshal(model)
	intent, err := parseNetworkReportIntent(raw)
	if err != nil {
		t.Fatal(err)
	}
	drift, err := c.reportIPAM(ctx, intent)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != 2 || string(drift[0].Change) != "Add" || string(drift[1].Change) != "Add" {
		t.Fatal("synthetic absence report failed")
	}
	t.Logf("PASS: global pools=%d reservations=%d; synthetic pool/reservation Add report via real site reference; no writes", len(pools), len(reservations))
}
