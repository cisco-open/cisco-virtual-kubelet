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

func TestLiveCatalystCenterInventoryReport(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	locations := map[string]string{}
	for _, site := range sites {
		assigned, err := c.assignedDevices(ctx, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		for id := range assigned {
			if locations[id] != "" {
				t.Fatal("ambiguous assignment")
			}
			locations[id] = site.NameHierarchy
		}
	}
	entries := []any{}
	for _, ip := range []string{"198.51.100.100", "198.51.100.101", "198.51.100.103"} {
		d, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil || locations[d.ID] == "" || d.Serial == "" {
			t.Fatal("lab assignment/identity missing", err)
		}
		entries = append(entries, map[string]any{"name": d.Hostname, "device_ip": ip, "serial_number": d.Serial, "state": "ASSIGN", "site": locations[d.ID]})
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": entries}}})
	if err != nil {
		t.Fatal("cannot encode qualification model")
	}
	plans, err := c.compileConfigReports(raw, []string{"inventory"})
	if err != nil || len(plans) != 1 {
		t.Fatal("report compilation failed", err)
	}
	drift, err := plans[0].observe(ctx)
	if err != nil || len(drift) != 0 {
		t.Fatal("live inventory assignment report did not converge", err)
	}
	intent, err := parseInventoryIntent(raw)
	if err != nil {
		t.Fatal(err)
	}
	executor := &deviceAssignmentExecutor{client: c, desired: intent.devices}
	if write, err := executor.next(ctx); err != nil || write != nil {
		t.Fatal("already assigned devices produced a mutation plan", err)
	}
	t.Log("native assignment executor independently confirmed convergence; no write submitted")
	t.Logf("compiled and observed ASSIGN model for %d lab devices; no drift; no mutations", len(entries))
}
