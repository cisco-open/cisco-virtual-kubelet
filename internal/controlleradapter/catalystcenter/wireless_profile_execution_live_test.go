// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Logical definitions only. This deliberately declares no site/tag assignment
// and leaves the SSID disabled. Uncertain outcomes retain exact fixture IDs.
func TestLiveWirelessProfileExecution(t *testing.T) {
	qualifyLiveWirelessProfileExecution(t, false, false, "")
}

func TestLiveWirelessProfileSiteTagsExecution(t *testing.T) {
	qualifyLiveWirelessProfileExecution(t, true, false, "")
}

func TestLiveWirelessProfileCustomZones(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_CUSTOM_ZONES") != "isolated-empty-sites" {
		t.Skip("explicit isolated custom-zone qualification not enabled")
	}
	qualifyLiveWirelessProfileExecution(t, true, true, "")
}

func TestLiveWirelessFlexProfileExecution(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_FLEX_PROFILE_EXECUTION") != "isolated-definitions" {
		t.Skip("explicit isolated FlexConnect profile qualification not enabled")
	}
	qualifyLiveWirelessProfileExecution(t, false, false, "flex")
}

func TestLiveWirelessFabricProfileExecution(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_FABRIC_PROFILE_EXECUTION") != "isolated-definitions" {
		t.Skip("explicit isolated Fabric profile qualification not enabled")
	}
	qualifyLiveWirelessProfileExecution(t, false, false, "fabric")
}

func qualifyLiveWirelessProfileExecution(t *testing.T, withTags, customZones bool, mode string) {
	if os.Getenv("CATC_WIRELESS_TAG_REPEAT") == "isolated-private" {
		// Refuse an incompatible qualification before creating any lab fixture.
		for name, want := range map[string]string{
			"CATC_WIRELESS_PROFILE_RECOVERY": "isolated-public",
			"CATC_WIRELESS_TAG_RECEIPTS":     "isolated-private",
			"CATC_WIRELESS_TAG_MEMBERSHIP":   "isolated-private",
			"CATC_WIRELESS_TAG_RECREATE":     "isolated-private",
			"CATC_WIRELESS_TAG_PRUNE":        "isolated-private",
			"CATC_PROFILE_SITE_PRUNE":        "isolated-public",
		} {
			if os.Getenv(name) != want {
				t.Fatal("repeat qualification requires explicit lifecycle gate", name)
			}
		}
		if !withTags || mode != "" || os.Getenv("CATC_PROFILE_TAG_DEPENDENCIES") == "isolated-private" {
			t.Fatal("repeat qualification requires both empty profile-site associations to remain assigned")
		}
	}
	if os.Getenv("CATC_WIRELESS_ZONE_BINDINGS") == "isolated-definitions" && (withTags || !customZones) {
		t.Fatal("zone-binding probe requires its dedicated definition-only custom-zone fixture")
	}
	if os.Getenv("CATC_WIRELESS_PROFILE_EXECUTION") != "isolated-definitions" {
		t.Skip("explicit isolated profile qualification not enabled")
	}
	if withTags && os.Getenv("CATC_WIRELESS_PROFILE_TAG_EXECUTION") != "isolated-empty-sites" {
		t.Skip("explicit empty-site tag qualification not enabled")
	}
	if withTags && os.Getenv("CATC_WIRELESS_PROFILE_RECOVERY") == "isolated-public" && os.Getenv("CATC_WIRELESS_TAG_RECEIPTS") == "isolated-private" && os.Getenv("CATC_PROFILE_SITE_PRUNE") == "isolated-public" && os.Getenv("CATC_WIRELESS_TAG_PRUNE") != "isolated-private" {
		t.Skip("private authoritative pruning requires explicit whole-tag qualification mode")
	}
	namedVLAN := !withTags && os.Getenv("CATC_WIRELESS_VLAN_SELECTOR") == "isolated-definitions"
	vlanName := os.Getenv("CATC_WIRELESS_VLAN_SELECTOR_NAME")
	vlanUpdate := os.Getenv("CATC_WIRELESS_VLAN_SELECTOR_UPDATE_NAME")
	if namedVLAN {
		if vlanName == "" {
			t.Skip("provide an existing design VLAN-group name")
		}
		if !validProfileName(vlanName) || vlanUpdate != "" && !validProfileName(vlanUpdate) {
			t.Fatal("invalid design VLAN-group selector")
		}
	}
	qualificationTimeout := 8 * time.Minute
	if os.Getenv("CATC_WIRELESS_PROFILE_RECOVERY") == "isolated-public" {
		qualificationTimeout = 15 * time.Minute
	}
	if os.Getenv("CATC_WIRELESS_TAG_PRUNE") == "isolated-private" {
		qualificationTimeout = 30 * time.Minute
	}
	if value := os.Getenv("CATC_WIRELESS_PROFILE_QUALIFICATION_TIMEOUT"); value != "" {
		parsed, err := time.ParseDuration(value)
		if err != nil || parsed < time.Minute || parsed > 90*time.Minute {
			t.Fatal("qualification timeout must be between 1m and 90m")
		}
		qualificationTimeout = parsed
	}
	ctx, cancel := context.WithTimeout(context.Background(), qualificationTimeout)
	defer cancel()
	c := liveClient(t)
	global, err := (&wirelessSSIDExecutor{client: c}).global(ctx)
	if err != nil {
		t.Fatal(err)
	}
	profiles, err := c.wirelessProfileInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ssids, err := c.wirelessSSIDInventory(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	var rfBaseline map[string]wirelessRFMetadata
	rfName, rfName2, rfName3 := "", "", ""
	extensionProbe := customZones && os.Getenv("CATC_POLICY_COHORT_EXTENSION_PROBE") == "isolated-empty-sites"
	extensionApplied := false
	if customZones {
		rfBaseline, err = c.wirelessRFProfiles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		candidates := []string{}
		for name, row := range rfBaseline {
			if row.fields["enableRadioTypeA"] == true {
				candidates = append(candidates, name)
			}
		}
		sort.Strings(candidates)
		if len(candidates) < 2 {
			t.Skip("two existing 5 GHz RF profiles required for isolated zone qualification")
		}
		rfName, rfName2 = candidates[0], candidates[1]
		if extensionProbe {
			if len(candidates) < 3 {
				t.Skip("three existing RF profiles required for cohort extension probe")
			}
			rfName3 = candidates[2]
			t.Log("existing third RF reference", rfName3, rfBaseline[rfName3].id)
		}
		t.Log("existing RF references", rfName, rfBaseline[rfName].id, rfName2, rfBaseline[rfName2].id)
	}
	profileName, ssidName := wirelessQualificationName(t, "CVK_N_"), wirelessQualificationName(t, "CVK_S_")
	t.Log("isolated names", profileName, ssidName)
	profileID, ssidID := "", ""
	ownedSites := map[string]string{}
	assigned := map[string]bool{}
	retainedTags := map[bool]map[string]wirelessProfileTagMetadata{false: {}, true: {}}
	var siteBaseline []Site
	if withTags {
		siteBaseline, err = c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}

	// Freeze unrelated public inventory and refuse any assignment, unknown field
	// or new reference to our SSID before every mutation, including cleanup.
	check := func() {
		t.Helper()
		if customZones {
			current, err := c.wirelessRFProfiles(ctx)
			if err != nil || !reflect.DeepEqual(rfBaseline, current) {
				t.Fatal("RF reference inventory changed; retain", err)
			}
		}
		ps, err := c.wirelessProfileInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ss, err := c.wirelessSSIDInventory(ctx, global)
		if err != nil {
			t.Fatal(err)
		}
		if withTags {
			current, err := c.ListSites(ctx)
			if err != nil {
				t.Fatal(err)
			}
			unrelated := []Site{}
			found := map[string]bool{}
			for _, site := range current {
				if id, owned := ownedSites[site.NameHierarchy]; owned {
					if site.ID != id || site.Type != "area" {
						t.Fatal("owned site changed; retain", site.NameHierarchy, id)
					}
					devices, err := c.assignedDevices(ctx, id)
					if err != nil || len(devices) != 0 {
						t.Fatal("site is not verifiably empty; retain", site.NameHierarchy, err)
					}
					found[site.NameHierarchy] = true
				} else {
					unrelated = append(unrelated, site)
				}
			}
			if len(found) != len(ownedSites) || !reflect.DeepEqual(siteBaseline, unrelated) {
				t.Fatal("site baseline changed; retain", ownedSites)
			}
		}
		if profileID != "" && ps[profileName].id != profileID {
			t.Fatal("owned profile disappeared or changed; retain fixtures", profileName, profileID)
		}
		for name, p := range ps {
			if p.unmapped {
				t.Fatal("unknown profile metadata; retain fixtures", profileName, ssidName)
			}
			if name == profileName {
				if profileID == "" || p.id != profileID {
					t.Fatal("unowned or replaced profile; retain fixtures", profileName, p.id)
				}
				sites, err := c.profileAssignedSites(ctx, p.id)
				if err != nil || !reflect.DeepEqual(sites, assigned) {
					t.Fatal("profile has assignment or unreadable scope", err)
				}
				for _, policy := range []bool{false, true} {
					tags, err := c.wirelessProfileTagInventory(ctx, p.id, policy)
					if err != nil || !reflect.DeepEqual(tags, retainedTags[policy]) {
						t.Fatal("profile tag scope changed", err)
					}
				}
				delete(ps, name)
			} else {
				entries, _ := p.fields["ssidDetails"].([]map[string]any)
				for _, entry := range entries {
					if entry["ssidName"] == ssidName {
						t.Fatal("foreign profile references test SSID; retain", ssidName)
					}
				}
			}
		}
		if profileID != "" {
			if _, exists := ps[profileName]; exists {
				t.Fatal("profile exclusion failed")
			}
		}
		if s, exists := ss[ssidName]; exists {
			if ssidID == "" || s.id != ssidID || s.fields["isEnabled"] != false || s.fields["authType"] != "OPEN" {
				t.Fatal("SSID identity/settings changed; retain", ssidName)
			}
			delete(ss, ssidName)
		} else if ssidID != "" {
			t.Fatal("owned SSID disappeared; retain", ssidName)
		}
		if !reflect.DeepEqual(profiles, ps) || !reflect.DeepEqual(ssids, ss) {
			t.Fatal("unrelated public baseline changed; retain", profileName, ssidName)
		}
	}
	if _, ok := profiles[profileName]; ok {
		t.Fatal("profile collision")
	}
	if _, ok := ssids[ssidName]; ok {
		t.Fatal("SSID collision")
	}
	check()
	row := ssidTestRow(ssidName)
	row["ssid_radio_type"] = "5GHz"
	row["enabled"] = false
	ssidCompiler := func() *wirelessConfiguration {
		t.Helper()
		e, err := c.compileWirelessConfiguration(ssidTestModel(row))
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	ssidExecutor := ssidCompiler()
	write, err := ssidExecutor.next(ctx)
	if err != nil || write == nil || write.action != "Create" || write.identity.target != "" {
		t.Fatal("SSID create plan", err)
	}
	var lastOperation *ciskov1.NetworkControllerConfigurationOperation
	var profileModel []byte
	var publicRecovery *liveNetworkProfileRecovery
	if os.Getenv("CATC_WIRELESS_PROFILE_RECOVERY") == "isolated-public" {
		publicRecovery = &liveNetworkProfileRecovery{}
	}
	run := func(e configurationExecutor, w *configurationWrite) {
		t.Helper()
		check()
		if _, profile := e.(*networkProfileConfiguration); profile && publicRecovery != nil {
			if _, _, supported := networkProfileMetadataResource(w.resource); supported {
				lastOperation = publicRecovery.run(t, ctx, c, profileModel, w)
				return
			}
		}
		submitted := time.Now()
		task, err := w.submit(ctx)
		if err != nil {
			t.Fatal("submission uncertain; retain", w.resource, err)
		}
		t.Log("task", w.resource, w.action, task.ID)
		if err = waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
			t.Fatal("task uncertain; retain", w.resource, err)
		}
		op := &ciskov1.NetworkControllerConfigurationOperation{Action: w.action, Resource: w.resource, ParentID: w.identity.parent, TargetID: w.identity.target, TaskID: task.ID}
		lastOperation = op.DeepCopy()
		if verified, err := e.verify(ctx, op); err != nil || !verified {
			t.Fatal("readback incomplete; retain", w.resource, err)
		}
	}
	run(ssidExecutor, write)
	currentSSID, err := c.wirelessSSIDInventory(ctx, global)
	if err != nil {
		t.Fatal(err)
	}
	ssidID = currentSSID[ssidName].id
	if !validAPIID(ssidID) {
		t.Fatal("SSID identity missing")
	}
	t.Log("owned SSID", ssidName, ssidID)
	if next, err := ssidCompiler().next(ctx); err != nil || next != nil {
		t.Fatal("fresh SSID compiler did not converge", err)
	}
	wlan := profileName
	localVLAN := 100
	paths, tagPaths := []string{}, []string{}
	associationReduced := false
	conflictKind := ""
	compile := func() *networkProfileConfiguration {
		t.Helper()
		row := map[string]any{"name": profileName, "ssid_details": []any{map[string]any{"name": ssidName, "wlan_profile_name": wlan}}}
		if namedVLAN {
			row["ssid_details"] = []any{map[string]any{"name": ssidName, "wlan_profile_name": wlan, "vlan_group_name": vlanName}}
		}
		if mode == "flex" {
			row["ssid_details"] = []any{map[string]any{"name": ssidName, "wlan_profile_name": wlan, "enable_fabric": false, "enable_flex_connect": true, "local_to_vlan": localVLAN}}
		}
		if mode == "fabric" {
			row["ssid_details"] = []any{map[string]any{"name": ssidName, "wlan_profile_name": wlan, "enable_fabric": true, "enable_flex_connect": false}}
		}
		if customZones {
			row["ap_zones"] = []any{
				map[string]any{"name": profileName + "_Z1", "rf_profile_name": rfName, "ssids": []string{ssidName}},
				map[string]any{"name": profileName + "_Z2", "rf_profile_name": rfName2, "ssids": []string{ssidName}},
			}
		}
		if extensionProbe {
			row["ap_zones"] = append(row["ap_zones"].([]any), map[string]any{"name": profileName + "_Z3", "rf_profile_name": rfName3, "ssids": []string{ssidName}})
		}
		if len(paths) > 0 {
			profileSites := paths
			if associationReduced {
				profileSites = tagPaths
			}
			row["sites"] = profileSites
		}
		if len(tagPaths) > 0 {
			row["site_tags"] = []any{map[string]any{"name": profileName + "_S", "sites": tagPaths, "ap_profile_name": "default-ap-profile"}}
			row["policy_tags"] = []any{map[string]any{"name": profileName + "_P", "sites": tagPaths}}
			if customZones {
				row["policy_tags"] = []any{
					map[string]any{"name": profileName + "_P1", "sites": tagPaths, "ap_zones": []string{profileName + "_Z1"}},
					map[string]any{"name": profileName + "_P2", "sites": tagPaths, "ap_zones": []string{profileName + "_Z2"}},
				}
			}
			if extensionApplied {
				row["policy_tags"] = append(row["policy_tags"].([]any), map[string]any{"name": profileName + "_P3", "sites": tagPaths, "ap_zones": []string{profileName + "_Z3"}})
			}
			if conflictKind != "" {
				other := map[string]any{"name": profileName + "_CONFLICT", "sites": tagPaths}
				if customZones && conflictKind == "policy_tags" {
					other["ap_zones"] = []string{profileName + "_Z1"}
				}
				if conflictKind == "site_tags" {
					other["ap_profile_name"] = "default-ap-profile"
				}
				row[conflictKind] = append(row[conflictKind].([]any), other)
			}

		}
		raw := wirelessProfileTestModel(row)
		profileModel = append([]byte{}, raw...)
		e, err := c.compileNetworkProfileConfiguration(raw)
		if err != nil {
			t.Fatal(err)
		}
		return e
	}
	e := compile()
	write, err = e.next(ctx)
	if err != nil || write == nil || write.action != "Create" || write.identity.target != "" {
		t.Fatal("profile create plan", err)
	}
	run(e, write)
	current, err := c.wirelessProfileInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	profileID = current[profileName].id
	if !validAPIID(profileID) {
		t.Fatal("created profile identity missing")
	}
	t.Log("owned profile", profileName, profileID)
	check()
	if next, err := compile().next(ctx); err != nil || next != nil {
		t.Fatal("fresh profile compiler did not converge", err)
	}
	wlan = profileName + "_2"
	if mode == "flex" {
		localVLAN = 200
	}
	if namedVLAN && vlanUpdate != "" {
		vlanName = vlanUpdate
	}
	e = compile()
	write, err = e.next(ctx)
	if err != nil || write == nil || write.action != "Update" || write.identity.target != profileID {
		t.Fatal("profile UUID-bound update plan", err)
	}
	run(e, write)
	check()
	if next, err := compile().next(ctx); err != nil || next != nil {
		t.Fatal("updated fresh compiler did not converge", err)
	}
	if os.Getenv("CATC_WIRELESS_ZONE_BINDINGS") == "isolated-definitions" {
		assertLiveWirelessZoneBindings(t, ctx, c, profileName, wirelessZoneBindings{
			ParentID: profileID, GlobalSiteID: global,
			Zones: map[string]wirelessZoneBinding{
				profileName + "_Z1": {RF: rfName, SSIDs: []string{ssidName}},
				profileName + "_Z2": {RF: rfName2, SSIDs: []string{ssidName}},
			},
			RFIDs:   map[string]string{rfName: rfBaseline[rfName].id, rfName2: rfBaseline[rfName2].id},
			SSIDIDs: map[string]string{ssidName: ssidID},
		})
	}
	if withTags {
		for _, suffix := range []string{"A", "B"} {
			name := profileName + "_" + suffix
			path := "Global/" + name
			for _, site := range siteBaseline {
				if site.NameHierarchy == path {
					t.Fatal("site collision", path)
				}
			}
			raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": map[string]any{"areas": []any{map[string]string{"name": name, "parent_name": "Global"}}}}})
			e, err := c.compileConfigurationExecutor(raw, "sites")
			if err != nil {
				t.Fatal(err)
			}
			w, err := e.next(ctx)
			if err != nil || w == nil || w.action != "Create" {
				t.Fatal("site create plan", err)
			}
			run(e, w)
			sites, err := c.ListSites(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, site := range sites {
				if site.NameHierarchy == path {
					ownedSites[path] = site.ID
				}
			}
			if !validAPIID(ownedSites[path]) {
				t.Fatal("created site identity missing", path)
			}
			t.Log("owned empty area", path, ownedSites[path])
			paths = append(paths, path)
			// Assign only the newly created empty area. No devices are assigned.
			eProfile := compile()
			w, err = eProfile.next(ctx)
			if err != nil || w == nil || w.action != "Update" || w.identity.parent != profileID || w.identity.target != ownedSites[path] {
				t.Fatal("site assignment plan", err)
			}
			run(eProfile, w)
			assigned[ownedSites[path]] = true
		}
		captureTags := func() {
			t.Helper()
			for _, policy := range []bool{false, true} {
				tags, err := c.wirelessProfileTagInventory(ctx, profileID, policy)
				if err != nil {
					t.Fatal(err)
				}
				for name, tag := range tags {
					expected := profileName + "_S"
					if policy {
						expected = profileName + "_P"
					}
					allowed := name == expected
					if policy && customZones {
						allowed = name == profileName+"_P1" || name == profileName+"_P2" || extensionApplied && name == profileName+"_P3"
					}
					if !allowed || tag.unmapped {
						t.Fatal("foreign or unmodeled tag; retain", name)
					}
					if old, exists := retainedTags[policy][name]; exists && old.id != tag.id {
						t.Fatal("tag replaced; retain", name)
					}
				}
				retainedTags[policy] = tags
			}
		}
		for i, action := range []string{"Create", "Update"} {
			tagPaths = append([]string{}, paths[:i+1]...)
			tagCount := 2
			if customZones && action == "Update" {
				tagCount = 3
			}
			if extensionApplied && action == "Update" {
				tagCount = 4
			}
			for range tagCount {
				eProfile := compile()
				w, err := eProfile.next(ctx)
				if err != nil || w == nil || w.action != action || w.identity.parent != profileID {
					t.Fatal("tag plan", action, err)
				}
				run(eProfile, w)
				captureTags()
				check()
			}
			if w, err := compile().next(ctx); err != nil || w != nil {
				t.Fatal("tag compiler did not converge", err)
			}
			qualifyLiveWirelessProfileTagRetainedReadback(t, ctx, c, compile(), lastOperation)
			for _, kind := range []string{"site_tags", "policy_tags"} {
				conflictKind = kind
				if next, err := compile().next(ctx); !errors.Is(err, errWirelessTagScopeConflict) || next != nil {
					t.Fatal("overlapping declaration was not refused", kind, err)
				}
				conflictKind = ""
				check()
			}
			t.Log("overlapping site and policy declarations refused with native inventories unchanged")

			if drift, err := compile().report(ctx); err != nil || len(drift) != 0 {
				t.Fatal("tag report drift", drift, err)
			}
			if extensionProbe && i == 0 {
				check()
				before, err := c.wirelessProfileTagInventory(ctx, profileID, true)
				if err != nil || len(before) != 2 {
					t.Fatal("unexpected extension baseline", err)
				}
				items := []map[string]any{}
				for _, suffix := range []string{"1", "2"} {
					tag := before[profileName+"_P"+suffix]
					if !validAPIID(tag.id) || tag.unmapped {
						t.Fatal("unqualified existing extension member")
					}
					items = append(items, cloneWirelessProfileValue(tag.fields).(map[string]any))
				}
				items = append(items, map[string]any{"policyTagName": profileName + "_P3", "siteIds": []string{ownedSites[paths[0]]}, "apZones": []string{profileName + "_Z3"}})
				body, _ := json.Marshal(map[string]any{"items": items})
				start := time.Now()
				raw, err := c.mutate(ctx, http.MethodPost, wirelessProfileTagPath(profileID, true)+"/bulk", body)
				if err != nil {
					t.Fatal("native extension probe outcome uncertain; retain", err)
				}
				var receipt struct {
					Response Task `json:"response"`
				}
				if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
					t.Fatal("missing extension receipt; retain")
				}
				t.Log("native mixed cohort probe task", receipt.Response.ID)
				if err = waitSiteCredentialTask(ctx, c, receipt.Response, start); err != nil {
					t.Fatal("extension task uncertain; retain", err)
				}
				now, err := c.wirelessProfileTagInventory(ctx, profileID, true)
				if err != nil || len(now) != 3 {
					t.Fatal("extension did not preserve all three members; retain", err)
				}
				for name, old := range before {
					if now[name].id != old.id {
						t.Fatal("extension replaced an existing tag; retain", name)
					}
				}
				extensionApplied = true
				captureTags()
				check()
				if next, err := compile().next(ctx); err != nil || next != nil {
					t.Fatal("extended native model did not converge", err)
				}
				t.Log("native bulk preserved existing tag IDs while adding the third member")
			}

		}
		if os.Getenv("CATC_WIRELESS_TAG_MEMBERSHIP") == "isolated-private" {
			if publicRecovery == nil || os.Getenv("CATC_WIRELESS_TAG_RECEIPTS") != "isolated-private" {
				t.Fatal("membership qualification requires private baseline recovery")
			}
			tagPaths = append([]string{}, paths[:1]...)
			associationReduced = os.Getenv("CATC_PROFILE_TAG_DEPENDENCIES") == "isolated-private"
			compile() // refresh the complete desired model; raw planning still refuses removals
			expected := 2
			if customZones {
				expected = 3
			}
			if removed := publicRecovery.removeTagMembership(t, ctx, c, profileModel, expected); removed != "" {
				delete(assigned, removed)
			}
			captureTags()
			check()
			if next, err := compile().next(ctx); err != nil || next != nil {
				t.Fatal("removed tag membership did not converge", err)
			}
		}
		t.Log("wireless empty-site assignment and site/policy tag create/additive-update/fresh-compiler/report verified")
		if os.Getenv("CATC_WIRELESS_TAG_RECREATE") == "isolated-private" {
			if publicRecovery == nil || !wirelessTagComparisonConfig(publicRecovery.config) {
				t.Fatal("native recreation requires private public-recovery qualification")
			}
			publicRecovery.recreateTags(t, ctx, c, len(retainedTags[false])+len(retainedTags[true]))
			if os.Getenv("CATC_WIRELESS_TAG_REPEAT") == "isolated-private" {
				publicRecovery.repeatTagRecreation(t, ctx, c, paths, len(retainedTags[false])+len(retainedTags[true]))
			}
			// The helper verifies distinct authenticated incarnations, including
			// stable native identifiers. Capture this incarnation for cleanup.
			for _, policy := range []bool{false, true} {
				clear(retainedTags[policy])
			}
			captureTags()
			check()
		}
	}
	if publicRecovery != nil {
		minimum := 2
		if withTags {
			minimum = 8
			if customZones {
				minimum = 9
			}
		}
		publicRecovery.finish(t, ctx, c, minimum)
	}
	remove := func(path, id string) {
		t.Helper()
		check()
		submitted := time.Now()
		raw, err := c.mutate(ctx, http.MethodDelete, path+"/"+url.PathEscape(id), nil)
		if err != nil {
			t.Fatal("cleanup uncertain; retain", id, err)
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Fatal("cleanup receipt missing", id)
		}
		t.Log("cleanup", id, receipt.Response.ID)
		if err = waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
			t.Fatal("cleanup task uncertain", id, err)
		}
	}
	if withTags {
		publicPrune := publicRecovery != nil && os.Getenv("CATC_PROFILE_SITE_PRUNE") == "isolated-public"
		if publicPrune {
			publicRecovery.assertPruneReferenced(t, ctx, c)
		}
		automaticTags := publicPrune && wirelessTagComparisonConfig(publicRecovery.config)
		if automaticTags {
			if os.Getenv("CATC_WIRELESS_TAG_PRUNE") != "isolated-private" {
				t.Fatal("private authoritative prune qualification requires explicit whole-tag mode")
			}
			count := len(retainedTags[false]) + len(retainedTags[true])
			remaining := len(paths)
			if associationReduced {
				remaining--
			}
			publicRecovery.pruneTagsAndAssociations(t, ctx, c, count, remaining)
			for _, policy := range []bool{true, false} {
				clear(retainedTags[policy])
			}
			for id := range assigned {
				delete(assigned, id)
			}
			check()
		}
		if !automaticTags {
			for _, policy := range []bool{true, false} {
				for name, tag := range retainedTags[policy] {
					remove(wirelessProfileTagPath(profileID, policy), tag.id)
					delete(retainedTags[policy], name)
				}
			}
		}
		if publicPrune && !automaticTags {
			remaining := len(paths)
			if associationReduced {
				remaining--
			}
			publicRecovery.prune(t, ctx, c, remaining)
		}
		for _, path := range paths {
			id := ownedSites[path]
			if !publicPrune {
				remove(profileSiteAssignmentsPath(profileID), id)
			}
			delete(assigned, id)
		}
	}
	remove(wirelessProfilesPath, profileID)
	current, err = c.wirelessProfileInventory(ctx)
	if err != nil || !reflect.DeepEqual(profiles, current) {
		t.Fatal("profile baseline not restored", err)
	}
	profileID = ""
	remove(wirelessSSIDPath(global), ssidID)
	ssidID = ""
	if withTags {
		for _, path := range paths {
			id := ownedSites[path]
			remove("/dna/intent/api/v1/areas", id)
			delete(ownedSites, path)
		}
	}
	check()
	t.Log("baselines restored; no device assignment, provisioning or device operation submitted")
}
