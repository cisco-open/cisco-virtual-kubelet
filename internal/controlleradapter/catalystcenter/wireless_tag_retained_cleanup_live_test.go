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
	"sort"
	"strings"
	"testing"
	"time"
)

// Supervised cleanup of an exact generated fixture after a terminal failed run.
// No journal adoption/replay or physical operation is performed.
func TestLiveWirelessTagRetainedFixtureCleanup(t *testing.T) {
	path := os.Getenv("CATC_WIRELESS_TAG_FIXTURE_CLEANUP")
	if path == "" {
		t.Skip("requires exact retained fixture identities")
	}
	var input struct {
		ProfileName, ProfileID, SSIDName, SSIDID, SiteTagID string
		Areas                                               map[string]string
		PolicyTags                                          map[string]string
		Assigned                                            []string
	}
	data, err := os.ReadFile(path)
	if err != nil || json.Unmarshal(data, &input) != nil || !strings.HasPrefix(input.ProfileName, "CVK_N_") || !strings.HasPrefix(input.SSIDName, "CVK_S_") || !validAPIID(input.ProfileID) || !validAPIID(input.SSIDID) || len(input.Areas) != 2 || len(input.Assigned) < 1 || len(input.Assigned) > 2 || len(input.PolicyTags) > 2 {
		t.Fatal("invalid exact generated fixture input", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	c := liveClient(t)
	global, err := (&wirelessSSIDExecutor{client: c}).global(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ssidPath := wirelessSSIDPath(global)
	getRows := func(path string) []json.RawMessage {
		t.Helper()
		rows, err := listInventoryQueryWithGet(ctx, func(ctx context.Context, path string, q url.Values) ([]byte, error) {
			return c.get(ctx, path, q, http.StatusOK)
		}, path, nil, func(raw json.RawMessage) string {
			var row struct {
				ID string `json:"id"`
			}
			json.Unmarshal(raw, &row)
			return row.ID
		})
		if err != nil {
			t.Fatal(err)
		}
		return rows
	}
	baseline := func(path string, excluded map[string]bool) []string {
		t.Helper()
		out := []string{}
		for _, raw := range getRows(path) {
			var row map[string]any
			if json.Unmarshal(raw, &row) != nil {
				t.Fatal("invalid baseline row")
			}
			id, _ := row["id"].(string)
			if !excluded[id] {
				value, _ := json.Marshal(row)
				out = append(out, string(value))
			}
		}
		sort.Strings(out)
		return out
	}
	areaIDs := map[string]bool{}
	for name, id := range input.Areas {
		if !strings.HasPrefix(name, "Global/"+input.ProfileName+"_") || !validAPIID(id) || areaIDs[id] {
			t.Fatal("invalid generated area identity")
		}
		areaIDs[id] = true
	}
	seenAssignments := map[string]bool{}
	for _, id := range input.Assigned {
		if !areaIDs[id] || seenAssignments[id] {
			t.Fatal("assignment is not an exact owned empty area")
		}
		seenAssignments[id] = true
	}
	if !validAPIID(input.SiteTagID) {
		t.Fatal("invalid generated site-tag identity")
	}
	siteBaseline := baseline(sitesPath, areaIDs)
	profileBaseline := baseline(wirelessProfilesPath, map[string]bool{input.ProfileID: true})
	ssidBaseline := baseline(ssidPath, map[string]bool{input.SSIDID: true})
	profilePresent, ssidPresent := true, true
	remainingTags := map[bool]map[string]string{false: {input.ProfileName + "_S": input.SiteTagID}, true: {}}
	for name, id := range input.PolicyTags {
		if !strings.HasPrefix(name, input.ProfileName+"_P") || !validAPIID(id) {
			t.Fatal("invalid generated policy-tag identity")
		}
		remainingTags[true][name] = id
	}
	tagBaselines := map[bool]map[string]wirelessProfileTagMetadata{}
	sort.Strings(input.Assigned)
	check := func() {
		t.Helper()
		sites, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for name, id := range input.Areas {
			found := 0
			for _, site := range sites {
				if site.ParentID == id {
					t.Fatal("generated area acquired a child; retain fixture")
				}
				if site.ID == id || site.NameHierarchy == name {
					if site.ID != id || site.NameHierarchy != name || site.Type != "area" {
						t.Fatal("generated area identity changed")
					}
					found++
				}
			}
			if found != 1 {
				t.Fatal("generated area missing or ambiguous")
			}
			ids = append(ids, id)
		}
		if len(ids) > 0 {
			if err := c.wirelessTagUnusedScopes(ctx, ids); err != nil {
				t.Fatal("generated scope has consumers or unreadable inventory", err)
			}
		}
		profiles, err := c.wirelessProfileInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if profilePresent {
			profile, exists := profiles[input.ProfileName]
			if !exists || profile.id != input.ProfileID || profile.unmapped {
				t.Fatal("generated profile identity changed")
			}
			for _, policy := range []bool{false, true} {
				rows, err := c.wirelessProfileTagInventory(ctx, input.ProfileID, policy)
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) != len(remainingTags[policy]) {
					t.Fatal("retained tag set changed")
				}
				for name, id := range remainingTags[policy] {
					row, ok := rows[name]
					if !ok || row.id != id || row.unmapped {
						t.Fatal("retained tag identity changed")
					}
					raw, _ := json.Marshal(row.fields["siteIds"])
					var ids []string
					if json.Unmarshal(raw, &ids) != nil || len(ids) < 1 || len(ids) > 2 {
						t.Fatal("unreadable retained tag scope")
					}
					for _, site := range ids {
						if !areaIDs[site] {
							t.Fatal("retained tag acquired a foreign scope")
						}
					}
				}
				if tagBaselines[policy] == nil {
					tagBaselines[policy] = rows
				} else if !reflect.DeepEqual(rows, tagBaselines[policy]) {
					t.Fatal("retained tag fields changed")
				}
			}
			var assigned struct {
				Response []struct {
					ID string `json:"id"`
				} `json:"response"`
			}
			raw, err := c.get(ctx, profileSiteAssignmentsPath(input.ProfileID), nil, http.StatusOK)
			if err != nil || json.Unmarshal(raw, &assigned) != nil {
				t.Fatal("assignment observation failed", err)
			}
			actual := []string{}
			for _, row := range assigned.Response {
				actual = append(actual, row.ID)
			}
			sort.Strings(actual)
			if !reflect.DeepEqual(actual, input.Assigned) {
				t.Fatal("generated profile assignments changed")
			}
		} else {
			for name, profile := range profiles {
				if name == input.ProfileName || profile.id == input.ProfileID {
					t.Fatal("profile deletion not independently observed; retain SSID")
				}
			}
		}
		for name, profile := range profiles {
			if name != input.ProfileName {
				data, _ := json.Marshal(profile.fields)
				if strings.Contains(string(data), input.SSIDName) {
					t.Fatal("another profile references the generated SSID")
				}
			}
		}
		{
			found := 0
			for _, raw := range getRows(ssidPath) {
				var row struct {
					ID, SSID  string
					IsEnabled *bool
				}
				if json.Unmarshal(raw, &row) != nil {
					t.Fatal("invalid SSID inventory")
				}
				if row.ID == input.SSIDID || row.SSID == input.SSIDName {
					if !ssidPresent {
						t.Fatal("SSID deletion not independently observed")
					}
					if row.ID != input.SSIDID || row.SSID != input.SSIDName || row.IsEnabled == nil || *row.IsEnabled {
						t.Fatal("generated disabled SSID identity changed")
					}
					found++
				}
			}
			if ssidPresent && found != 1 {
				t.Fatal("generated SSID missing or ambiguous")
			}
		}
	}
	remove := func(path string) {
		t.Helper()
		check()
		at := time.Now()
		raw, err := c.mutate(ctx, http.MethodDelete, path, nil)
		if err != nil {
			t.Fatal("cleanup uncertain; do not replay", err)
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Fatal("cleanup acknowledgement missing")
		}
		if err := waitSiteCredentialTask(ctx, c, receipt.Response, at); err != nil {
			t.Fatal("cleanup task uncertain; retain remaining fixture", err)
		}
		t.Log("exact retained cleanup task", path, receipt.Response.ID)
	}
	for _, policy := range []bool{true, false} {
		names := []string{}
		for name := range remainingTags[policy] {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			remove(wirelessProfileTagPath(input.ProfileID, policy) + "/" + url.PathEscape(remainingTags[policy][name]))
			delete(remainingTags[policy], name)
			delete(tagBaselines[policy], name)
		}
	}
	for len(input.Assigned) > 0 {
		remove(profileSiteAssignmentsPath(input.ProfileID) + "/" + input.Assigned[0])
		input.Assigned = input.Assigned[1:]
	}
	remove(wirelessProfilesPath + "/" + input.ProfileID)
	profilePresent = false
	remove(ssidPath + "/" + url.PathEscape(input.SSIDID))
	ssidPresent = false
	for name, id := range input.Areas {
		remove("/dna/intent/api/v1/areas/" + id)
		delete(input.Areas, name)
	}
	if !reflect.DeepEqual(baseline(sitesPath, nil), siteBaseline) || !reflect.DeepEqual(baseline(wirelessProfilesPath, nil), profileBaseline) || !reflect.DeepEqual(baseline(ssidPath, nil), ssidBaseline) {
		t.Fatal("unrelated baseline changed during exact fixture cleanup")
	}
	t.Log("retained generated fixture removed; unrelated site/profile/SSID baselines preserved; no physical operation submitted")
}
