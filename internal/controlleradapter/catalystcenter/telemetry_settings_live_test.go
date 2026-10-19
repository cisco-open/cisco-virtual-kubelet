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

func TestLiveCatalystCenterTelemetryReport(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var global networkObservation
	for _, site := range sites {
		if site.Type == "global" {
			global, err = c.readNetworkSetting(ctx, site.ID, "telemetry")
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, group := range telemetryGroups {
		if len(global.telemetry[group]) == 0 {
			t.Fatal("qualification requires explicit global telemetry groups")
		}
	}
	g := global.telemetry
	profile := map[string]any{
		"name":                                 "qualification",
		"wired_data_collection":                g["wiredDataCollection"]["enableWiredDataCollection"],
		"wireless_telemetry":                   g["wirelessTelemetry"]["enableWirelessTelemetry"],
		"catalyst_center_as_snmp_server":       g["snmpTraps"]["useBuiltinTrapServer"],
		"snmp_servers":                         g["snmpTraps"]["externalTrapServers"],
		"catalyst_center_as_syslog_server":     g["syslogs"]["useBuiltinSyslogServer"],
		"syslog_servers":                       g["syslogs"]["externalSyslogServers"],
		"catalyst_center_as_network_collector": g["applicationVisibility"]["collectorType"] == "Builtin",
		"enable_netflow_collector_on_devices":  g["applicationVisibility"]["enableOnWiredAccessDevices"],
	}
	if profile["catalyst_center_as_network_collector"] == false {
		profile["netflow_collector_ip_address"] = g["applicationVisibility"]["address"]
		profile["netflow_collector_port"] = g["applicationVisibility"]["port"]
	}
	explicit, inherited, unset := 0, 0, 0
	for _, site := range sites {
		actual, err := c.readNetworkSetting(ctx, site.ID, "telemetry")
		if err != nil {
			t.Fatal(err)
		}
		for _, group := range telemetryGroups {
			if actual.telemetry[group] == nil {
				inherited++
			} else if len(actual.telemetry[group]) == 0 {
				unset++
			} else {
				explicit++
			}
		}
		ref := map[string]any{"network_settings": map[string]any{"telemetry": "qualification"}}
		refs := map[string]any{"global": ref}
		if site.Type != "global" {
			ref["name"] = site.Name
			ref["parent_name"] = parentSitePath(site.NameHierarchy)
			refs = map[string]any{site.Type + "s": []any{ref}}
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": refs, "network_settings": map[string]any{"telemetry": []any{profile}}}})
		desired, err := parseNetworkSettings(raw)
		if err != nil {
			t.Fatal(err)
		}
		expected := telemetryDrift(site.NameHierarchy, global.telemetry, actual.telemetry)
		drift, err := c.reportNetworkSettings(ctx, desired)
		if err != nil || len(drift) != len(expected) {
			t.Fatalf("report failed: err=%v drift=%d expected=%d", err, len(drift), len(expected))
		}
		if site.Type == "global" && len(drift) != 0 {
			t.Fatal("global round trip differs")
		}
	}
	changed := global.telemetry["wiredDataCollection"]["enableWiredDataCollection"].(bool)
	synthetic := map[string]map[string]any{}
	for k, v := range global.telemetry {
		synthetic[k] = v
	}
	synthetic["wiredDataCollection"] = map[string]any{"enableWiredDataCollection": !changed}
	if len(telemetryDrift("Global", synthetic, global.telemetry)) != 1 {
		t.Fatal("synthetic drift missed")
	}
	t.Logf("PASS: sites=%d telemetry groups explicit=%d inherited=%d unset=%d; model round trip and synthetic drift; no writes", len(sites), explicit, inherited, unset)
}
