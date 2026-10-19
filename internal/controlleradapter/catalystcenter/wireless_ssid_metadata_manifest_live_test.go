//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestLiveWirelessOpenSSIDMetadataManifest(t *testing.T) {
	if os.Getenv("CATC_OPEN_SSID_METADATA_MANIFEST") != "isolated-disabled-definition" {
		t.Skip("isolated disabled OPEN manifest qualification not enabled")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	c := liveClient(t)
	site, err := (&wirelessSSIDExecutor{client: c}).global(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := c.wirelessSSIDInventory(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := c.wirelessProfileInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	name := wirelessQualificationName(t, "CVK_O_")
	if _, exists := baseline[name]; exists {
		t.Fatal("generated name already exists")
	}
	id := ""
	h := newWirelessMetadataManifest(t, ctx, c, func(path string) bool { return id != "" && path == wirelessSSIDPath(site)+"/"+id }, 1)
	check := func() {
		t.Helper()
		currentProfiles, err := c.wirelessProfileInventory(ctx)
		if err != nil || !reflect.DeepEqual(profiles, currentProfiles) {
			t.Fatal("wireless profile baseline changed", err)
		}
		for _, p := range currentProfiles {
			if p.unmapped {
				t.Fatal("unknown wireless profile dependencies")
			}
			for _, collection := range []string{"ssidDetails", "apZones", "featureTemplates"} {
				entries, _ := p.fields[collection].([]map[string]any)
				for _, entry := range entries {
					if entry["ssidName"] == name {
						t.Fatal("generated SSID is referenced")
					}
					if names, ok := entry["ssids"].([]string); ok {
						for _, selected := range names {
							if selected == name {
								t.Fatal("generated SSID is referenced")
							}
						}
					}
				}
			}
		}
		current, err := c.wirelessSSIDInventory(ctx, site)
		if err != nil {
			t.Fatal(err)
		}
		if observed, exists := current[name]; exists {
			if id == "" || observed.id != id || observed.fields["isEnabled"] != false || observed.fields["authType"] != "OPEN" {
				t.Fatal("generated SSID identity or disabled OPEN state changed")
			}
			delete(current, name)
		}
		if !reflect.DeepEqual(baseline, current) {
			t.Fatal("unrelated SSID baseline changed")
		}
	}
	row := ssidTestRow(name)
	row["enabled"] = false
	row["ssid_radio_type"] = "5GHz"
	for _, timeout := range []int{0, 600} {
		if timeout != 0 {
			row["session_timeout"] = timeout
		}
		raw := ssidTestModel(row)
		executor, err := c.compileWirelessConfiguration(raw)
		if err != nil {
			t.Fatal(err)
		}
		write, err := executor.next(ctx)
		if err != nil || write == nil || len(write.identity.credentials) != 0 {
			t.Fatal("metadata-only manifest plan", err)
		}
		if timeout == 0 && (write.action != "Create" || write.identity.target != "") {
			t.Fatal("new fixture was not a create-only plan")
		}
		if timeout != 0 && (write.action != "Update" || write.identity.target != id) {
			t.Fatal("update lost original UUID")
		}
		check()
		task := h.submit(t, ctx, raw, write)
		if timeout == 0 {
			current, err := c.wirelessSSIDInventory(ctx, site)
			if err != nil {
				t.Fatal(err)
			}
			id = current[name].id
			if !validAPIID(id) {
				t.Fatal("created SSID UUID unavailable")
			}
			t.Log("generated disabled OPEN UUID", name, id)
		}
		if next, err := executor.next(ctx); err != nil || next != nil {
			t.Fatal("native metadata readback/no-op failed", err)
		}
		h.noOp(t, ctx, raw)
		h.noOp(t, ctx, raw)
		check()
		t.Log("public metadata-only SSID converged", timeout, task.ID)
	}
	check()
	submitted := time.Now()
	raw, err := c.mutate(ctx, http.MethodDelete, wirelessSSIDPath(site)+"/"+url.PathEscape(id), nil)
	if err != nil {
		t.Fatal("exact cleanup uncertain; retain", name, id, err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("cleanup acknowledgement missing")
	}
	if err := waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
		t.Fatal("cleanup task incomplete", err)
	}
	check()
	current, err := c.wirelessSSIDInventory(ctx, site)
	if err != nil || !reflect.DeepEqual(baseline, current) {
		t.Fatal("SSID baseline not restored", err)
	}
	h.completed(t)
	t.Log("disabled OPEN metadata-only create/update/restart/no-op and exact cleanup qualified", id, receipt.Response.ID)
}
