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
	"strings"
	"testing"
	"time"
)

// Supervised exact-ID cleanup of generated fixtures after qualification stops.
// This neither adopts a profile nor adds automatic provider prune authority.
func TestLiveWirelessAPBoundaryFixtureCleanup(t *testing.T) {
	file := os.Getenv("CATC_AP_BOUNDARY_FIXTURE_CLEANUP")
	if file == "" {
		t.Skip("provide an exact generated power-fixture identity file")
	}
	var wanted struct {
		APName string
		Powers map[string]string
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &wanted) != nil || !strings.HasPrefix(wanted.APName, "CVK_C_") || len(wanted.Powers) != 2 {
		t.Fatal("invalid isolated cleanup scope")
	}
	for name, id := range wanted.Powers {
		if (!strings.HasPrefix(name, "CVK_P_") && !strings.HasPrefix(name, "CVK_Q_")) || !validAPIID(id) {
			t.Fatal("unbound generated power fixture")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := liveClient(t)
	aps, err := c.wirelessAPMetadataInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := aps[wanted.APName]; exists {
		t.Fatal("AP create may have occurred; independent cleanup authority required")
	}
	baseline, err := c.wirelessPowerProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for name, id := range wanted.Powers {
		if baseline[name].ID != id {
			t.Fatal("original generated power UUID changed")
		}
		delete(baseline, name)
	}
	check := func() {
		t.Helper()
		currentAP, err := c.wirelessAPMetadataInventory(ctx)
		if err != nil || !reflect.DeepEqual(aps, currentAP) {
			t.Fatal("AP baseline changed; retain", err)
		}
		for _, ap := range currentAP {
			if name, ok := ap.fields["apPowerProfileName"].(string); ok && wanted.Powers[name] != "" {
				t.Fatal("generated power profile is referenced")
			}
			if calendars, ok := ap.fields["calendarPowerProfiles"].([]any); ok {
				for _, value := range calendars {
					row, ok := value.(map[string]any)
					if !ok {
						t.Fatal("unresolved calendar reference")
					}
					name, _ := row["powerProfileName"].(string)
					if wanted.Powers[name] != "" {
						t.Fatal("generated power calendar is referenced")
					}
				}
			}
		}
		current, err := c.wirelessPowerProfiles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for name, id := range wanted.Powers {
			if row, exists := current[name]; exists {
				if row.ID != id {
					t.Fatal("power UUID replaced")
				}
				delete(current, name)
			}
		}
		if !reflect.DeepEqual(baseline, current) {
			t.Fatal("unrelated power baseline changed")
		}
	}
	for name, id := range wanted.Powers {
		check()
		current, err := c.wirelessPowerProfiles(ctx)
		if err != nil || current[name].ID != id {
			t.Fatal("original power missing before exact cleanup", err)
		}
		submitted := time.Now()
		raw, err := c.mutate(ctx, http.MethodDelete, wirelessPowerPath+"/"+url.PathEscape(id), nil)
		if err != nil {
			t.Fatal("cleanup uncertain; do not replay", name, id, err)
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Fatal("cleanup acknowledgement unavailable", name, id)
		}
		if err := waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
			t.Fatal("cleanup task incomplete; retain", name, id, err)
		}
		t.Log("exact generated power cleanup", name, id, receipt.Response.ID)
	}
	check()
	current, err := c.wirelessPowerProfiles(ctx)
	if err != nil || !reflect.DeepEqual(current, baseline) {
		t.Fatal("original baseline not restored", err)
	}
	t.Log("generated prerequisites removed; original AP/power baselines restored")
}
