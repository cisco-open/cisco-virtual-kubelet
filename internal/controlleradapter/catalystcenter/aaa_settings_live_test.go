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

func TestLiveCatalystCenterAAAReport(t *testing.T) {
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
	for _, site := range sites {
		actual, err := c.readNetworkSetting(ctx, site.ID, "aaa")
		if err != nil {
			t.Fatal(err)
		}
		profile := map[string]any{"name": "qualification"}
		expected := 0
		for _, group := range aaaGroups {
			observed := actual.aaa[group.api]
			fields := map[string]any{}
			switch {
			case observed == nil:
				inherited++
				expected++
			case len(observed) == 0:
				unset++
				expected++
			default:
				explicit++
			}
			if len(observed) == 0 {
				fields = map[string]any{"server_type": "AAA", "protocol": "RADIUS", "primary_ip": "192.0.2.240"}
			} else {
				for _, field := range aaaFields {
					if value, ok := observed[field.api]; ok {
						fields[field.model] = value
					}
				}
			}
			profile[group.model] = fields
		}
		ref := map[string]any{"network_settings": map[string]any{"aaa_servers": "qualification"}}
		refs := map[string]any{"global": ref}
		if site.Type != "global" {
			ref["name"] = site.Name
			ref["parent_name"] = parentSitePath(site.NameHierarchy)
			refs = map[string]any{site.Type + "s": []any{ref}}
		}
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": refs, "network_settings": map[string]any{"aaa_servers": []any{profile}}}})
		desired, err := parseNetworkSettings(raw)
		if err != nil {
			t.Fatal(err)
		}
		drift, err := c.reportNetworkSettings(ctx, desired)
		if err != nil || len(drift) != expected {
			t.Fatalf("AAA report: err=%v drift=%d expected=%d", err, len(drift), expected)
		}
	}
	t.Logf("PASS: sites=%d AAA groups explicit=%d inherited=%d unset=%d; model reporting; shared secrets excluded; no writes", len(sites), explicit, inherited, unset)
}
