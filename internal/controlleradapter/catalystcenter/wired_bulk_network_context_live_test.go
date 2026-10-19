// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Test-only source fixtures derive from a read-only native snapshot. Production
// never fills missing declarations from native state. No accepted task or
// installed worker proof is fabricated by this qualification.
func TestLiveCatalystCenterWiredBulkNetworkContext(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trace := &wiredBulkReadOnlyTransport{base: rest.HTTPClient.Transport}
	rest.HTTPClient.Transport = trace
	t.Cleanup(func() {
		if trace.attempts.Load() != 0 {
			t.Error("network context qualification attempted a configuration mutation")
		}
	})
	members, _, _ := liveWiredBulkMembers(t, c, ctx)
	catalog, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Site{}
	for _, site := range catalog {
		byPath[site.NameHierarchy] = site
	}
	geometry := liveWiredBulkResolvedSites(t, catalog)
	profiles := []any{}
	rows := []any{}
	seen := map[string]bool{}
	kinds := map[string]bool{}
	for _, member := range members {
		rows = append(rows, map[string]any{"name": member.desired.reference.Name, "device_ip": member.desired.reference.IP, "serial_number": member.desired.reference.Serial, "site": member.desired.site, "state": "REPROVISION"})
		if seen[member.desired.site] {
			continue
		}
		seen[member.desired.site] = true
		name := fmt.Sprintf("observed-profile-%d", len(profiles))
		profile := map[string]any{"name": name}
		for _, kind := range []string{"ntp", "dns", "timezone"} {
			observed, err := c.effectiveNetworkSetting(ctx, byPath, member.desired.site, kind)
			if err != nil {
				t.Fatal("effective native setting unavailable", err)
			}
			if observed.inherited || observed.unset {
				continue
			}
			switch kind {
			case "ntp":
				if len(observed.servers) == 0 {
					continue
				}
				profile["ntp_servers"] = observed.servers
			case "dns":
				if len(observed.servers) == 0 || observed.domain == "" {
					continue
				}
				profile["dns_servers"], profile["domain_name"] = observed.servers, observed.domain
			case "timezone":
				if observed.timezone == "" {
					continue
				}
				profile["timezone"] = observed.timezone
			}
			kinds[kind] = true
		}
		if len(profile) == 1 {
			t.Skip("no settled NTP/DNS/timezone policy for a lab device; native context success remains unqualified")
		}
		profiles = append(profiles, profile)
		bound := false
		for _, entries := range geometry {
			for _, entry := range entries {
				if entry["parent_name"].(string)+"/"+entry["name"].(string) == member.desired.site {
					entry["network_settings"] = map[string]any{"network": name}
					bound = true
				}
			}
		}
		if !bound {
			t.Fatal("native site was not declared by the explicit source fixture")
		}
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": rows}, "sites": geometry, "network_settings": map[string]any{"network": profiles}}})
	if err != nil {
		t.Fatal(err)
	}
	source := &configSourceSnapshot{raw: raw}
	before := bytes.Clone(raw)
	model, err := compileWiredBulkSource(source)
	if err != nil {
		t.Fatal("resolved source rejected", err)
	}
	batches, err := groupWiredBulkMembers(model.inventory, model.states, model.sites, wiredBulkGrouping{})
	if err != nil {
		t.Fatal(err)
	}
	if err := c.checkWiredBulkNetworkContext(ctx, source, batches); err != nil {
		t.Fatal("pinned source does not match effective native policy", err)
	}
	// Change only a separate desired-source fixture. This never changes the
	// appliance's settings and must be detected without a provisioning write.
	first := profiles[0].(map[string]any)
	if _, present := first["ntp_servers"]; present {
		replacement := "192.0.2.254"
		if strings.Contains(fmt.Sprint(first["ntp_servers"]), replacement) {
			replacement = "192.0.2.253"
		}
		first["ntp_servers"] = []string{replacement}
	} else if value, present := first["domain_name"]; present {
		first["domain_name"] = "changed.example.test"
		if value == first["domain_name"] {
			first["domain_name"] = "other.example.test"
		}
	} else {
		original := first["timezone"]
		first["timezone"] = "Europe/Berlin"
		if original == "Europe/Berlin" {
			first["timezone"] = "Etc/UTC"
		}
	}
	changedRaw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": rows}, "sites": geometry, "network_settings": map[string]any{"network": profiles}}})
	changed := &configSourceSnapshot{raw: changedRaw}
	if err := c.checkWiredBulkNetworkContext(ctx, changed, batches); err == nil {
		t.Fatal("different declared policy accepted")
	}
	if err := c.checkWiredBulkNetworkContext(ctx, source, batches); err != nil || !bytes.Equal(before, source.raw) {
		t.Fatal("original native/source policy changed during refusal", err)
	}
	readyQueries, unsupportedQueries := 0, 0
	for _, batch := range batches {
		expected, err := compileWiredBulkNetworkContext(source, batch)
		if err != nil {
			t.Fatal(err)
		}
		for _, settings := range expected {
			if _, err := wiredBulkNetworkCommands(settings); err != nil {
				if !errors.Is(err, errWiredBulkNetworkContextUnavailable) {
					t.Fatal("unexpected worker plan failure", err)
				}
				unsupportedQueries++
			} else {
				readyQueries++
			}
		}
	}
	t.Logf("source-derived worker query plans supported=%d unsupported=%d; no worker results read and no device timezone/banner proof claimed", readyQueries, unsupportedQueries)
	t.Logf("three live switch identities: scoped original context matched, changed source refused, original context rechecked; setting kinds=%v site scopes=%d batches=%d native configuration writes=0; no device convergence claimed", kinds, len(seen), len(batches))
}
