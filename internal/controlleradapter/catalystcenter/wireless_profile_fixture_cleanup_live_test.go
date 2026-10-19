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

// Explicit exact-ID cleanup of generated lab fixtures after qualification stops.
// This is supervised test tooling, not provider adoption or automatic pruning.
func TestLiveWirelessProfileFixtureCleanup(t *testing.T) {
	path := os.Getenv("CATC_WIRELESS_PROFILE_FIXTURE_CLEANUP")
	if path == "" {
		t.Skip("provide exact generated fixture identity file")
	}
	var want struct {
		ProfileName, ProfileID, SSIDName, SSIDID string
		Sites                                    map[string]string
		Tags                                     map[string]string
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(raw, &want) != nil || !strings.HasPrefix(want.ProfileName, "CVK_N_") || !strings.HasPrefix(want.SSIDName, "CVK_S_") || !validAPIID(want.ProfileID) || !validAPIID(want.SSIDID) || len(want.Sites) != 2 || (len(want.Tags) != 0 && len(want.Tags) != 2) {
		t.Fatal("invalid exact generated-fixture scope")
	}
	for _, suffix := range []string{"_A", "_B"} {
		if !validAPIID(want.Sites["Global/"+want.ProfileName+suffix]) {
			t.Fatal("unbound generated area")
		}
	}
	for _, suffix := range []string{"_S", "_P"} {
		if len(want.Tags) > 0 && !validAPIID(want.Tags[want.ProfileName+suffix]) {
			t.Fatal("unbound generated tag")
		}
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	global := ""
	for _, site := range sites {
		if site.NameHierarchy == "Global" && site.Type == "global" {
			if global != "" {
				t.Fatal("duplicate Global")
			}
			global = site.ID
		}
	}
	if !validAPIID(global) {
		t.Fatal("missing Global")
	}
	profiles, err := c.wirelessProfileInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ssids, err := c.wirelessSSIDInventory(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	profile, exists := profiles[want.ProfileName]
	if exists && (profile.id != want.ProfileID || profile.unmapped) {
		t.Fatal("original profile missing, replaced or unmodeled; retain")
	}
	ssid, exists := ssids[want.SSIDName]
	if exists && (ssid.id != want.SSIDID || ssid.fields["isEnabled"] != false || ssid.fields["authType"] != "OPEN") {
		t.Fatal("original disabled OPEN SSID changed; retain")
	}
	for name, p := range profiles {
		if p.id == want.ProfileID && name != want.ProfileName {
			t.Fatal("original profile renamed")
		}
	}
	for name, p := range ssids {
		if p.id == want.SSIDID && name != want.SSIDName {
			t.Fatal("original SSID renamed")
		}
	}
	originalProfiles := map[string]wirelessProfileMetadata{}
	for name, p := range profiles {
		if name != want.ProfileName {
			originalProfiles[name] = p
			entries, _ := p.fields["ssidDetails"].([]map[string]any)
			for _, entry := range entries {
				if entry["ssidName"] == want.SSIDName {
					t.Fatal("foreign profile references generated SSID; retain")
				}
			}
		}
	}
	delete(ssids, want.SSIDName)
	originalSSIDs := ssids
	unrelatedSites := []Site{}
	ids := map[string]bool{}
	for _, id := range want.Sites {
		ids[id] = true
	}
	for _, site := range sites {
		if id, ok := want.Sites[site.NameHierarchy]; ok {
			if site.ID != id || site.Type != "area" {
				t.Fatal("generated site identity changed")
			}
			devices, err := c.assignedDevices(ctx, id)
			if err != nil || len(devices) != 0 {
				t.Fatal("generated site has devices or unknown evidence", err)
			}
		} else {
			if ids[site.ID] {
				t.Fatal("generated area renamed; retain")
			}
			unrelatedSites = append(unrelatedSites, site)
		}
	}
	assigned := map[string]bool{}
	expectedTags := map[bool]map[string]wirelessProfileTagMetadata{false: {}, true: {}}
	if profile.id != "" {
		assigned, err = c.profileAssignedSites(ctx, want.ProfileID)
		if err != nil {
			t.Fatal(err)
		}
		for id := range assigned {
			if !ids[id] {
				t.Fatal("foreign site assignment; retain")
			}
		}
		for _, policy := range []bool{false, true} {
			tags, err := c.wirelessProfileTagInventory(ctx, want.ProfileID, policy)
			if err != nil {
				t.Fatal(err)
			}
			if len(tags) > 1 {
				t.Fatal("unexpected tag population; retain")
			}
			for name, tag := range tags {
				if tag.unmapped || tag.id != want.Tags[name] {
					t.Fatal("foreign or changed tag; retain")
				}
				members, _ := tag.fields["siteIds"].([]string)
				for _, id := range members {
					if !ids[id] {
						t.Fatal("tag references foreign site; retain")
					}
				}
			}
			expectedTags[policy] = tags
		}

	}
	// Revalidate complete generated identities and all unrelated baselines before
	// every mutation. Only expected removals update these local snapshots.
	check := func() {
		t.Helper()
		current, err := c.wirelessProfileInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if profile.id != "" {
			p, ok := current[want.ProfileName]
			if !ok || !reflect.DeepEqual(p, profile) {
				t.Fatal("profile changed during cleanup")
			}
			delete(current, want.ProfileName)
		}
		if !reflect.DeepEqual(current, originalProfiles) {
			t.Fatal("unrelated profiles changed")
		}
		currentSSIDs, err := c.wirelessSSIDInventory(ctx, global)
		if err != nil {
			t.Fatal(err)
		}
		if ssid.id != "" {
			actual, ok := currentSSIDs[want.SSIDName]
			if !ok || !reflect.DeepEqual(actual, ssid) {
				t.Fatal("SSID changed during cleanup")
			}
			delete(currentSSIDs, want.SSIDName)
		}
		if !reflect.DeepEqual(currentSSIDs, originalSSIDs) {
			t.Fatal("unrelated SSIDs changed")
		}
		currentSites, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		others := []Site{}
		for _, site := range currentSites {
			if id, ok := want.Sites[site.NameHierarchy]; ok {
				if site.ID != id {
					t.Fatal("area changed during cleanup")
				}
				devices, err := c.assignedDevices(ctx, id)
				if err != nil || len(devices) > 0 {
					t.Fatal("area no longer empty", err)
				}
			} else {
				others = append(others, site)
			}
		}
		if !reflect.DeepEqual(others, unrelatedSites) {
			t.Fatal("unrelated site baseline changed")
		}
		pnp, err := c.pnpDevices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, device := range pnp {
			id := device.Info.SiteID
			if id == "" {
				id = device.Info.Location.SiteID
			}
			if ids[id] || id == "" && (device.Info.State != "Unclaimed" || device.Info.OnboardingState != "Not Contacted" || device.Info.WorkflowID != "" || device.Workflow != nil || device.SystemWorkflow != nil || device.ResetWorkflow != nil) {
				t.Fatal("onboarding reference or unknown scope; retain")
			}
		}
		if profile.id != "" {
			got, err := c.profileAssignedSites(ctx, profile.id)
			if err != nil || !reflect.DeepEqual(got, assigned) {
				t.Fatal("assignments changed during cleanup", err)
			}
			for _, policy := range []bool{false, true} {
				got, err := c.wirelessProfileTagInventory(ctx, profile.id, policy)
				if err != nil || !reflect.DeepEqual(got, expectedTags[policy]) {
					t.Fatal("tag snapshot changed during cleanup", err)
				}
			}
		}
	}
	remove := func(path, id string) {
		t.Helper()
		check()
		start := time.Now()
		raw, err := c.mutate(ctx, http.MethodDelete, path+"/"+url.PathEscape(id), nil)
		if err != nil {
			t.Fatal("exact cleanup outcome uncertain; retain remaining scope", err)
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Fatal("missing exact cleanup task")
		}
		if err := waitSiteCredentialTask(ctx, c, receipt.Response, start); err != nil {
			t.Fatal(err)
		}
		t.Log("exact fixture cleanup", id, receipt.Response.ID)
	}
	for _, policy := range []bool{true, false} {
		for name, tag := range expectedTags[policy] {
			remove(wirelessProfileTagPath(want.ProfileID, policy), tag.id)
			delete(expectedTags[policy], name)
		}
	}
	for id := range assigned {
		remove(profileSiteAssignmentsPath(want.ProfileID), id)
		delete(assigned, id)
		fresh, err := c.wirelessProfileInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		profile = fresh[want.ProfileName]
	}
	if profile.id != "" {
		remove(wirelessProfilesPath, want.ProfileID)
	}
	profile.id = ""
	check()
	if ssid.id != "" {
		remove(wirelessSSIDPath(global), want.SSIDID)
	}
	ssid.id = ""
	check()
	for path, id := range want.Sites {
		current, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		present := false
		for _, site := range current {
			if site.ID == id {
				if site.NameHierarchy != path {
					t.Fatal("area renamed before deletion")
				}
				present = true
			}
		}
		if present {
			remove("/dna/intent/api/v1/areas", id)
		}
		delete(want.Sites, path)
	}
	check()
	t.Log("exact generated profile/tags/assignments/disabled-SSID/empty-area cleanup and unrelated baselines verified; no physical action")
}
