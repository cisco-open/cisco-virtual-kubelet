// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestLiveSDATransitInventoryAliases(t *testing.T) {
	if os.Getenv("CATC_SDA_TRANSIT_QUALIFICATION") != "read-only-aliases" {
		t.Skip("explicit read-only alias qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal("session unavailable")
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	writes := &atomic.Int64{}
	rest.HTTPClient.Transport = livePlannedAPTrace{base: base, t: t, writes: writes}
	defer func() { rest.HTTPClient.Transport = base }()
	inventory, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal("native inventory unavailable")
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal("native sites unavailable")
	}
	paths := map[string]string{}
	fallback := ""
	for _, site := range sites {
		if site.Type != "global" && validSitePath(site.NameHierarchy) {
			paths[site.ID] = site.NameHierarchy
			if fallback == "" {
				fallback = site.NameHierarchy
			}
		}
	}
	if fallback == "" {
		t.Fatal("no native dependency site available for alias qualification")
	}
	allDevices, allTransits := []any{}, []any{}
	missingProvision := false
	for i, ip := range []string{"198.51.100.100", "198.51.100.101", "198.51.100.103"} {
		d, err := resolveConfigurationDevice(configurationDeviceReference{IP: ip}, inventory)
		if err != nil {
			t.Fatal("lab identity absent or ambiguous", ip)
		}
		provisioned, ready, err := c.wiredProvisionedDevice(ctx, d.ID)
		if err != nil {
			t.Fatal("native provisioning read failed", ip)
		}
		site := fallback
		if ready {
			site = paths[provisioned.SiteID]
			if site == "" {
				t.Fatal("provisioned site absent", ip)
			}
		}
		alias := fmt.Sprintf("CVK_LAB_CP_%d", i)
		row := map[string]any{"name": alias, "fqdn_name": d.Hostname, "device_ip": d.ManagementIP, "serial_number": d.Serial, "site": site, "state": "PROVISION"}
		allDevices = append(allDevices, row)
		allTransits = append(allTransits, map[string]any{"name": fmt.Sprintf("CVK-ReadOnly-Source-%d", i), "type": "SDA_LISP_PUB_SUB_TRANSIT", "control_plane_devices": []string{alias}})
		missingProvision = missingProvision || !ready
		raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": []any{row}}, "fabric": map[string]any{"transits": []any{map[string]any{"name": "CVK-ReadOnly-Alias-Probe", "type": "SDA_LISP_PUB_SUB_TRANSIT", "control_plane_devices": []string{alias}}}}}})
		wants, err := parseSDATransitIntents(raw)
		if err != nil || len(wants) != 1 {
			t.Fatal("live alias dependency model did not compile")
		}
		ref := wants[0].references[alias]
		resolved, err := resolveWiredProvisionTarget(inventoryDeviceIntent{reference: ref.selector, site: ref.site}, inventory)
		if err != nil || resolved.ID != d.ID || resolved.Hostname != d.Hostname {
			t.Fatal("alias did not resolve exact native identity", ip)
		}
		snapshot, preflightErr := c.sdaTransitSnapshot(ctx, wants[0])
		if !ready && (!errors.Is(preflightErr, errConfigurationBusy) || snapshot != nil) {
			t.Fatal("unprovisioned alias bypassed prerequisite", ip)
		}
		if ready && preflightErr != nil {
			t.Fatal("alias native prerequisite readback did not converge", ip)
		}
		t.Log("live alias resolves exact native identity", ip, "provisionedRecord", ready, "preflightReady", preflightErr == nil)
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": allDevices}, "fabric": map[string]any{"transits": allTransits}}})
	wants, err := parseSDATransitIntents(raw)
	if err != nil || len(wants) != 3 {
		t.Fatal("three-declaration source did not compile")
	}
	whole, err := c.sdaTransitSourceSnapshot(ctx, wants)
	if missingProvision {
		if !errors.Is(err, errConfigurationBusy) || whole != nil {
			t.Fatal("whole source bypassed a missing provisioned CP")
		}
	} else if err != nil || len(whole.entries) != 3 {
		t.Fatal("whole-source native observation failed")
	}
	t.Log("three-declaration native source preflight assessed", "missingProvisioning", missingProvision, "sourceReady", err == nil)
	if writes.Load() != 0 {
		t.Fatal("read-only alias qualification emitted a product mutation")
	}
	t.Log("three live model aliases resolved; native provisioning prerequisites enforced; zero product writes, no CLI/worker/SDA execution claim")
}
