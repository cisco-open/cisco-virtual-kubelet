// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Recovery never resubmits a create/update or reconstructs discarded secrets.
// It accepts only the saved acknowledged update plus original application UUID.
func TestLiveRetainedSSIDManifestCleanup(t *testing.T) {
	dir := os.Getenv("CATC_SSID_MANIFEST_RETAINED")
	if dir == "" {
		t.Skip("no retained SSID fixture supplied")
	}
	var identity ssidQualificationIdentity
	raw, err := os.ReadFile(filepath.Join(dir, "identity.json"))
	if err != nil || json.Unmarshal(raw, &identity) != nil {
		t.Fatal("retained identity unavailable")
	}
	var state cvk.NetworkControllerConfigurationStatus
	raw, err = os.ReadFile(filepath.Join(dir, "journal.json"))
	if err != nil || json.Unmarshal(raw, &state) != nil {
		t.Fatal("retained journal unavailable")
	}
	source, err := os.ReadFile(filepath.Join(dir, "source.json"))
	if err != nil {
		t.Fatal("retained source unavailable", err)
	}
	if err := validateSSIDQualificationRecovery(identity, &state, source); err != nil {
		t.Fatal("retained qualification proof refused", err)
	}
	op := state.Operation
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	c := liveClient(t)
	cleanupPath := filepath.Join(dir, "cleanup.json")
	if raw, err := os.ReadFile(cleanupPath); err == nil {
		var previous qualificationCleanup
		if json.Unmarshal(raw, &previous) != nil || validateSSIDQualificationCleanup(identity, op, previous) != nil {
			t.Fatal("earlier delete attempted without recoverable task; no replay")
		}
		rest, _, err := c.session(ctx)
		if err != nil {
			t.Fatal(err)
		}
		base := rest.HTTPClient.Transport
		if base == nil {
			base = http.DefaultTransport
		}
		readonly := &liveSSIDManifestTrace{base: base, path: wirelessSSIDPath(identity.SiteID), name: identity.Name, id: identity.ID, posts: 1}
		rest.HTTPClient.Transport = readonly
		if err := waitSiteCredentialTask(ctx, c, Task{ID: previous.DeleteTaskID}, previous.SubmittedAt); err != nil {
			t.Fatal("existing delete outcome unresolved; no replay", err)
		}
		waitSSIDFixtureAbsent(t, c, ctx, identity.SiteID, identity.Name, identity.ID)
		for range 2 {
			assertSSIDQualificationOriginalBaseline(t, c, ctx, dir, identity.SiteID, "")
		}
		previous.Deleted = true
		raw, _ := json.Marshal(previous)
		if err := os.WriteFile(cleanupPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
		if readonly.attempts != 0 {
			t.Fatal("read-only cleanup resume attempted a mutation")
		}
		t.Log("existing exact delete task recovered read-only; no deletion replay", identity.Name, previous.DeleteTaskID)
		return
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := waitSiteCredentialTask(ctx, c, Task{ID: op.TaskID}, op.SubmittedAt.Time); err != nil {
		t.Fatal("retained update outcome unresolved; no cleanup", err)
	}
	// The accepted task may finish before collection readback settles. Poll
	// only the selected public identity/settings; never repeat its update.
	desired, err := parseWirelessSSIDs(source)
	if err != nil {
		t.Fatal(err)
	}
	stable := false
	for range 30 {
		rows, err := c.wirelessSSIDInventory(ctx, identity.SiteID)
		if err != nil {
			t.Fatal(err)
		}
		if row, present := rows[identity.Name]; present {
			if row.id != identity.ID || row.fields["isEnabled"] != false || row.fields["authType"] != "WPA2_PERSONAL" {
				t.Fatal("retained SSID changed; no cleanup")
			}
			if wirelessSSIDMatches(desired[0].metadata, row.fields) {
				stable = true
				break
			}
		}
		select {
		case <-ctx.Done():
			t.Fatal("retained readback unresolved; no cleanup")
		case <-time.After(time.Second):
		}
	}
	if !stable {
		t.Fatal("retained definition did not settle; no cleanup")
	}
	baseline, err := c.wirelessSSIDInventory(ctx, identity.SiteID)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := c.wirelessProfileInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		rows, err := c.wirelessSSIDInventory(ctx, identity.SiteID)
		if err != nil {
			t.Fatal(err)
		}
		owned, exists := rows[identity.Name]
		if !exists || owned.id != identity.ID || owned.fields["isEnabled"] != false || owned.fields["authType"] != "WPA2_PERSONAL" || !reflect.DeepEqual(rows, baseline) {
			t.Fatal("retained SSID identity changed; no cleanup")
		}
		current, err := c.wirelessProfileInventory(ctx)
		if err != nil || !reflect.DeepEqual(profiles, current) {
			t.Fatal("profile baseline changed; no cleanup", err)
		}
		for _, profile := range current {
			for _, collection := range []string{"ssidDetails", "apZones", "featureTemplates"} {
				entries, _ := profile.fields[collection].([]map[string]any)
				for _, entry := range entries {
					if entry["ssidName"] == identity.Name {
						t.Fatal("retained fixture has a profile reference")
					}
					if names, ok := entry["ssids"].([]string); ok {
						for _, name := range names {
							if name == identity.Name {
								t.Fatal("retained fixture has a profile reference")
							}
						}
					}
				}
			}
		}
	}
	for range 2 {
		assertSSIDQualificationOriginalBaseline(t, c, ctx, dir, identity.SiteID, identity.Name)
	}
	// Guard the sole allowed DELETE; authentication and reads remain unchanged.
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	trace := &liveSSIDManifestTrace{base: base, path: wirelessSSIDPath(identity.SiteID), name: identity.Name, id: identity.ID, posts: 1, puts: identity.Puts, allowDelete: true}
	rest.HTTPClient.Transport = trace
	submitted := time.Now()
	cleanup := qualificationCleanup{Name: identity.Name, ID: identity.ID, UpdateTaskID: op.TaskID, SubmittedAt: submitted, DeleteAttempted: true}
	if err := claimQualificationCleanup(cleanupPath, cleanup); err != nil {
		t.Fatal("delete already claimed or evidence unavailable; no replay", err)
	}
	raw, err = c.mutate(ctx, http.MethodDelete, trace.path+"/"+url.PathEscape(identity.ID), nil)
	if err != nil {
		t.Fatal("exact cleanup unresolved", err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("invalid cleanup receipt")
	}
	cleanup.DeleteTaskID = receipt.Response.ID
	saveCleanup := func() {
		raw, _ := json.Marshal(cleanup)
		if err := os.WriteFile(cleanupPath, raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	saveCleanup()
	if err := waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
		t.Fatal(err)
	}
	waitSSIDFixtureAbsent(t, c, ctx, identity.SiteID, identity.Name, identity.ID)
	delete(baseline, identity.Name)
	for range 2 {
		after, err := c.wirelessSSIDInventory(ctx, identity.SiteID)
		if err != nil || !reflect.DeepEqual(baseline, after) {
			t.Fatal("remaining SSID baseline changed", err)
		}
	}
	currentProfiles, err := c.wirelessProfileInventory(ctx)
	if err != nil || !reflect.DeepEqual(profiles, currentProfiles) {
		t.Fatal("profile baseline changed", err)
	}
	for range 2 {
		assertSSIDQualificationOriginalBaseline(t, c, ctx, dir, identity.SiteID, "")
	}
	cleanup.Deleted = true
	saveCleanup()
	t.Log("retained acknowledged update task completed; exact disabled unassociated SSID removed without replay", identity.Name, identity.ID, op.TaskID, receipt.Response.ID)
}

func assertSSIDQualificationOriginalBaseline(t *testing.T, c *client, ctx context.Context, dir, site, exclude string) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "baseline.json"))
	if err != nil {
		t.Fatal(err)
	}
	var original any
	if err := json.Unmarshal(raw, &original); err != nil {
		t.Fatal(err)
	}
	rows, err := c.wirelessSSIDInventory(ctx, site)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := c.wirelessProfileInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ssids := map[string]any{}
	publicProfiles := map[string]any{}
	for name, row := range rows {
		if name != exclude {
			ssids[name] = map[string]any{"id": row.id, "fields": row.fields}
		}
	}
	for name, row := range profiles {
		publicProfiles[name] = map[string]any{"id": row.id, "fields": row.fields}
	}
	raw, _ = json.Marshal(map[string]any{"ssids": ssids, "profiles": publicProfiles})
	var current any
	if err := json.Unmarshal(raw, &current); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, current) {
		t.Fatal("original qualification baseline changed; no further mutation")
	}
}
