// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func wirelessQualificationName(t *testing.T, prefix string) string {
	t.Helper()
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	return prefix + hex.EncodeToString(suffix[:])
}

// This explicitly enabled live test qualifies isolated logical definitions only.
// It never assigns a profile, provisions a controller or operates on an AP.
func TestLiveWirelessAPCalendar(t *testing.T) {
	if os.Getenv("CATC_WIRELESS_AP_CALENDAR_EXECUTION") != "isolated-definitions" {
		t.Skip("isolated calendar definition test not enabled")
	}
	duration := 5 * time.Minute
	if os.Getenv("CATC_WIRELESS_AP_MANIFEST_BOUNDARIES_EXECUTION") == "isolated-definitions" {
		duration = 8 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()
	c := liveClient(t)
	apName, powerName := wirelessQualificationName(t, "CVK_C_"), wirelessQualificationName(t, "CVK_P_")
	powerNames := []string{powerName, wirelessQualificationName(t, "CVK_Q_")}
	t.Log("isolated names", apName, powerNames)
	aps, err := c.wirelessAPMetadataInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	powers, err := c.wirelessPowerProfiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	networks, err := c.wirelessProfileInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := aps[apName]; exists {
		t.Fatal("AP fixture name already exists")
	}
	for _, name := range powerNames {
		if _, exists := powers[name]; exists {
			t.Fatal("power fixture name already exists")
		}
	}
	apID := ""
	powerIDs := map[string]string{}
	// Inspect all known references before each mutation. Unknown tag metadata or
	// baseline drift holds the test; no fixture is automatically deleted on an
	// uncertain task/identity outcome.
	check := func() {
		t.Helper()
		currentAP, err := c.wirelessAPMetadataInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		currentPower, err := c.wirelessPowerProfiles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		currentNetworks, err := c.wirelessProfileInventory(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(networks, currentNetworks) {
			t.Fatal("wireless profile baseline changed; retain fixtures", apName, powerName)
		}
		for _, profile := range currentNetworks {
			if profile.unmapped {
				t.Fatal("unknown wireless profile fields prevent isolation proof")
			}
			tags, err := c.wirelessProfileTagInventory(ctx, profile.id, false)
			if err != nil {
				t.Fatal(err)
			}
			for _, tag := range tags {
				if tag.unmapped || tag.fields["apProfileName"] == apName {
					t.Fatal("unknown or assigned AP profile scope; retain fixture", apName)
				}
			}
		}
		if apID == "" {
			if _, exists := currentAP[apName]; exists {
				t.Fatal("unowned AP name appeared; refuse mutation")
			}
		} else {
			if currentAP[apName].id != apID {
				t.Fatal("AP UUID changed; retain fixture", apName, apID)
			}
			delete(currentAP, apName)
		}
		for _, name := range powerNames {
			id := powerIDs[name]
			if id == "" {
				if _, exists := currentPower[name]; exists {
					t.Fatal("unowned power name appeared; refuse mutation", name)
				}
			} else {
				if currentPower[name].ID != id {
					t.Fatal("power UUID changed; retain fixture", name, id)
				}
				delete(currentPower, name)
			}
		}
		if !reflect.DeepEqual(aps, currentAP) || !reflect.DeepEqual(powers, currentPower) {
			t.Fatal("unrelated definition baseline changed; retain fixtures", apName, powerName)
		}
	}
	var manifest *wirelessMetadataManifest
	if os.Getenv("CATC_WIRELESS_AP_MANIFEST_BOUNDARIES_EXECUTION") == "isolated-definitions" {
		manifest = newWirelessMetadataManifest(t, ctx, c, func(path string) bool {
			if apID != "" && path == wirelessAPPath+"/"+apID {
				return true
			}
			for _, id := range powerIDs {
				if id != "" && path == wirelessPowerPath+"/"+id {
					return true
				}
			}
			return false
		}, 3)
	}
	var latestRaw []byte
	calendar := func(kind string) []any {
		if kind == "NEGATIVE_TIMEZONE" || kind == "COUNTRY" || kind == "BOUNDARY_RESET" || kind == "CLEAR" || kind == "STATIC_POWER" || kind == "SECURITY_FEATURES" || kind == "REMOTE_WORKER" || kind == "MESH" || kind == "RESET" {
			return []any{}
		}
		if kind == "MULTIPLE" || kind == "REORDERED" || kind == "OVERLAP" || kind == "WEEKLY_DISJOINT" || kind == "MONTHLY_BOUNDARIES" {
			entries := []any{
				map[string]any{"power_profile": powerNames[0], "scheduler_type": "DAILY", "scheduler_start_time": "22:00", "scheduler_end_time": "23:00"},
				map[string]any{"power_profile": powerNames[1], "scheduler_type": "DAILY", "scheduler_start_time": "20:00", "scheduler_end_time": "21:00"},
			}
			if kind == "OVERLAP" {
				entries[1].(map[string]any)["scheduler_start_time"] = "22:30"
				entries[1].(map[string]any)["scheduler_end_time"] = "23:30"
			}
			if kind == "WEEKLY_DISJOINT" {
				for i, days := range [][]string{{"monday", "friday"}, {"saturday", "sunday"}} {
					row := entries[i].(map[string]any)
					row["scheduler_type"] = "WEEKLY"
					row["scheduler_day"] = days
				}
			}
			if kind == "MONTHLY_BOUNDARIES" {
				for i, dates := range [][]string{{"01", "31"}, {"02", "30"}} {
					row := entries[i].(map[string]any)
					row["scheduler_type"] = "MONTHLY"
					row["scheduler_date"] = dates
				}
			}
			if kind == "REORDERED" {
				entries[0], entries[1] = entries[1], entries[0]
			}
			return entries
		}
		entry := map[string]any{"power_profile": powerName, "scheduler_type": kind, "scheduler_start_time": "22:00", "scheduler_end_time": "23:00"}
		if kind == "WEEKLY" {
			entry["scheduler_day"] = []string{"monday", "friday"}
		}
		if kind == "MONTHLY" {
			entry["scheduler_date"] = []string{"2", "12"}
		}
		return []any{entry}
	}
	compile := func(kind string) *wirelessConfiguration {
		t.Helper()
		powerRows := []any{}
		for _, name := range powerNames {
			powerRows = append(powerRows, map[string]any{"name": name, "rules": []any{map[string]any{"interface_type": "ETHERNET", "interface_id": "GIGABITETHERNET0", "parameter_type": "SPEED", "parameter_value": "1000MBPS"}}})
		}
		profile := map[string]any{"name": apName, "description": "CVK isolated calendar qualification", "time_zone": "Controller", "country_code": "US", "calendar_power_profiles": calendar(kind)}
		switch kind {
		case "NEGATIVE_TIMEZONE":
			profile["time_zone"] = "Delta from Controller"
			profile["time_zone_offset_hour"] = -5
			profile["time_zone_offset_minutes"] = 30
		case "COUNTRY":
			profile["country_code"] = "CA"
		case "STATIC_POWER":
			profile["power_profile"] = powerNames[0]
			profile["client_limit"] = 10
		case "SECURITY_FEATURES":
			profile["cdp_state"] = true
			profile["awips_enabled"] = true
			profile["awips_forensic_enabled"] = true
			profile["pmf_denial_enabled"] = true
			profile["rogue_detection"] = true
			profile["rogue_detection_min_rssi"] = -80
			profile["rogue_detection_transient_interval"] = 120
			profile["rogue_detection_report_interval"] = 20
		case "REMOTE_WORKER":
			profile["remote_worker_enabled"] = true
		case "MESH":
			profile["mesh_enabled"] = true
			profile["bridge_group_name"] = "CVKBGN"
			profile["backhaul_client_access"] = true
			profile["range"] = 200
			profile["ghz5_backhaul_data_rates"] = "802.11ax"
			profile["ghz24_backhaul_data_rates"] = "802.11ax"
			profile["rap_downlink_backhaul"] = "5 GHz"
			profile["time_zone"] = "Delta from Controller"
			profile["time_zone_offset_hour"] = 1
			profile["time_zone_offset_minutes"] = 30
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"wireless": map[string]any{
			"power_profiles": powerRows,
			"ap_profiles":    []any{profile},
		}}})
		if err != nil {
			t.Fatal(err)
		}
		latestRaw = append([]byte(nil), raw...)
		result, err := c.compileWirelessConfiguration(raw)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	submit := func(e *wirelessConfiguration, w *configurationWrite, action, target string) {
		t.Helper()
		if w == nil || w.action != action || w.identity.target != target || len(w.identity.credentials) != 0 {
			t.Fatal("unexpected isolated definition plan")
		}
		check()
		submitted := time.Now()
		var task Task
		var err error
		if manifest != nil {
			task = manifest.submit(t, ctx, latestRaw, w)
		} else {
			task, err = w.submit(ctx)
		}
		if err != nil {
			t.Fatal("submission uncertain; retain fixtures", apName, powerName, err)
		}
		t.Log("definition task", w.resource, w.action, task.ID)
		if err := waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
			t.Fatal("task incomplete; retain fixtures", apName, powerName, err)
		}
		op := &cvk.NetworkControllerConfigurationOperation{Resource: w.resource, Action: w.action, TargetID: w.identity.target, TaskID: task.ID}
		done, err := e.verify(ctx, op)
		if err != nil || !done {
			t.Fatal("native readback incomplete; retain fixtures", apName, powerName, err)
		}
	}
	e := compile("DAILY")
	for range len(powerNames) {
		first, err := e.next(ctx)
		if err != nil {
			t.Fatal(err)
		}
		selected := ""
		for _, name := range powerNames {
			if first != nil && strings.HasSuffix(first.resource, "power-profile:"+name) && powerIDs[name] == "" {
				selected = name
			}
		}
		if selected == "" {
			t.Fatal("all declared power prerequisites must precede AP profile")
		}
		submit(e, first, "Create", "")
		currentPower, err := c.wirelessPowerProfiles(ctx)
		if err != nil {
			t.Fatal(err)
		}
		powerIDs[selected] = currentPower[selected].ID
		if !validAPIID(powerIDs[selected]) {
			t.Fatal("created power UUID unavailable")
		}
		t.Log("owned power UUID", selected, powerIDs[selected])
	}
	next, err := e.next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if next == nil || next.resource != "ap-profile:"+apName {
		t.Fatal("AP calendar create plan missing")
	}
	submit(e, next, "Create", "")
	currentAP, err := c.wirelessAPMetadataInventory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	apID = currentAP[apName].id
	if !validAPIID(apID) {
		t.Fatal("created AP UUID unavailable")
	}
	t.Log("owned AP UUID", apID)
	stages := []string{"DAILY", "WEEKLY", "MONTHLY", "MULTIPLE", "REORDERED", "CLEAR"}
	if os.Getenv("CATC_WIRELESS_AP_SETTINGS_EXECUTION") == "isolated-definitions" {
		stages = append(stages, "STATIC_POWER", "SECURITY_FEATURES", "MESH", "RESET")
	}
	if os.Getenv("CATC_WIRELESS_AP_BOUNDARIES_EXECUTION") == "isolated-definitions" {
		stages = append(stages, "WEEKLY_DISJOINT", "MONTHLY_BOUNDARIES", "NEGATIVE_TIMEZONE", "COUNTRY", "OVERLAP", "BOUNDARY_RESET")
	}
	for _, kind := range stages {
		e = compile(kind)
		next, err = e.next(ctx)
		if err != nil {
			t.Fatal("calendar plan", kind, err)
		}
		if kind != "DAILY" && next != nil {
			submit(e, next, "Update", apID)
		} else if kind != "DAILY" && kind != "REORDERED" {
			t.Fatal("calendar transition unexpectedly had no plan", kind)
		} else if next != nil {
			t.Fatal("daily no-op attempted a write")
		}
		if next, err = e.next(ctx); err != nil || next != nil {
			t.Fatal("calendar no-op failed", kind, err)
		}
		// Recompile to simulate a restart without keeping executor comparison state.
		if next, err = compile(kind).next(ctx); err != nil || next != nil {
			t.Fatal("calendar restart did not converge", kind, err)
		}
		if manifest != nil {
			manifest.noOp(t, ctx, latestRaw)
		}
		check()
		t.Log("qualified calendar", kind, "same UUID and unchanged baselines")
	}
	deleteOwned := func(path, id, label string) {
		t.Helper()
		check()
		submitted := time.Now()
		raw, err := c.mutate(ctx, http.MethodDelete, path+"/"+url.PathEscape(id), nil)
		if err != nil {
			t.Fatal("cleanup uncertain; retain identity", label, id, err)
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Fatal("cleanup acknowledgement missing", label, id)
		}
		t.Log("cleanup task", label, id, receipt.Response.ID)
		if err := waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
			t.Fatal("cleanup task incomplete", label, id, err)
		}
	}
	deleteOwned(wirelessAPPath, apID, "AP")
	apID = ""
	for i := len(powerNames) - 1; i >= 0; i-- {
		name := powerNames[i]
		deleteOwned(wirelessPowerPath, powerIDs[name], name)
		powerIDs[name] = ""
	}
	check()
	if manifest != nil {
		manifest.completed(t)
	}
	t.Log("baseline restored; no assignment, provisioning or device action submitted")
}
