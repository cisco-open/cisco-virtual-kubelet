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

// Read-only qualification; synthetic desired values never leave the planner.
func TestLiveCatalystCenterNetworkSettingsReport(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	explicit, inherited, unset := 0, 0, 0
	bindings := []networkBinding{}
	expectedDrift := 0
	byPath := map[string]Site{}
	for _, site := range sites {
		byPath[site.NameHierarchy] = site
	}
	for _, site := range sites {
		binding := networkBinding{path: site.NameHierarchy, kind: site.Type}
		for _, kind := range []string{"ntp", "dhcp", "dns"} {
			got, err := c.readNetworkSetting(ctx, site.ID, kind)
			if err != nil {
				t.Fatalf("%s settings read: %v", kind, err)
			}
			switch {
			case got.inherited:
				inherited++
			case got.unset:
				unset++
			default:
				explicit++
			}
			// Profiles declare effective values. The appliance can collapse a
			// matching override into inheritance; an inherited group is not
			// automatically drift, while an unset group always is.
			got, err = c.effectiveNetworkSetting(ctx, byPath, site.NameHierarchy, kind)
			if err != nil {
				t.Fatalf("%s effective settings read: %v", kind, err)
			}
			want := networkSetting{kind: kind, servers: got.servers, domain: got.domain}
			if got.unset {
				expectedDrift++
				if kind == "dns" {
					want.domain = "cvk-qualification.invalid"
				}
			}
			binding.settings = append(binding.settings, want)
			synthetic := networkSetting{kind: kind, servers: []string{"192.0.2.254"}, domain: "cvk-qualification.invalid"}
			if len(got.servers) == 1 && got.servers[0] == "192.0.2.254" {
				synthetic.servers = []string{"192.0.2.253"}
			}
			if len(networkSettingDrift(site.NameHierarchy, synthetic, got)) == 0 {
				t.Fatal("synthetic drift missed")
			}
		}
		bindings = append(bindings, binding)
	}
	if len(bindings) == 0 {
		t.Fatal("no sites available")
	}
	drift, err := c.reportNetworkSettings(ctx, bindings)
	if err != nil {
		t.Fatal(err)
	}
	if len(drift) != expectedDrift {
		t.Fatalf("report drift count=%d expected=%d", len(drift), expectedDrift)
	}
	t.Logf("PASS: sites=%d explicit=%d inherited=%d unset=%d; complete reporting and synthetic drift; no configuration writes", len(sites), explicit, inherited, unset)
}

// Qualify actual scalar reads and parsing through the same NetAsCode profile contract.
func TestLiveCatalystCenterNetworkScalarReport(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) == 0 {
		t.Fatal("no sites")
	}
	explicit, inherited, unset := 0, 0, 0
	byPath := map[string]Site{}
	for _, site := range sites {
		byPath[site.NameHierarchy] = site
	}
	for _, site := range sites {
		for _, kind := range []string{"timezone", "banner"} {
			got, err := c.readNetworkSetting(ctx, site.ID, kind)
			if err != nil {
				t.Fatalf("%s read failed: %v", kind, err)
			}
			switch {
			case got.inherited:
				inherited++
			case got.unset:
				unset++
			default:
				explicit++
			}
			got, err = c.effectiveNetworkSetting(ctx, byPath, site.NameHierarchy, kind)
			if err != nil {
				t.Fatalf("%s effective settings read: %v", kind, err)
			}
			expected := 0
			if got.unset {
				expected = 1
			}
			value := got.timezone
			if kind == "banner" {
				value = got.banner
			}
			if kind == "timezone" && value == "" {
				value = "GMT"
			}
			if kind == "banner" && got.bannerType != "Custom" {
				expected = 1
			}
			ref := map[string]any{"network_settings": map[string]any{"network": "qualification"}}
			refs := map[string]any{"global": ref}
			if site.Type != "global" {
				ref["name"] = site.Name
				ref["parent_name"] = parentSitePath(site.NameHierarchy)
				refs = map[string]any{site.Type + "s": []any{ref}}
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": refs, "network_settings": map[string]any{"network": []any{map[string]any{"name": "qualification", kind: value}}}}})
			desired, err := parseNetworkSettings(raw)
			if err != nil {
				t.Fatal(err)
			}
			drift, err := c.reportNetworkSettings(ctx, desired)
			if err != nil || len(drift) != expected {
				t.Fatalf("%s report: err=%v drift=%d expected=%d", kind, err, len(drift), expected)
			}
			want := desired[0].settings[0]
			if kind == "timezone" {
				want.timezone = "GMT"
				if got.timezone == "GMT" {
					want.timezone = "Europe/Berlin"
				}
			} else {
				want.banner = got.banner + " synthetic local change"
			}
			if len(networkSettingDrift(site.NameHierarchy, want, got)) != 1 {
				t.Fatal("synthetic change missed")
			}
		}
	}
	t.Logf("PASS: sites=%d explicit=%d inherited=%d unset=%d; timezone/banner model reports and synthetic drift; no writes", len(sites), explicit, inherited, unset)
}
