// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func ewlcComposedFixture(t *testing.T, mode string, intercept ...func(http.ResponseWriter, *http.Request) bool) (*client, fabricEWLCIntent) {
	t.Helper()
	raw := strings.Replace(fabricEdgeIntentModel, `["EDGE_NODE"]`, `["EDGE_NODE","EMBEDDED_WIRELESS_CONTROLLER_NODE"]`, 1)
	raw = strings.Replace(raw, `"name":"edge"`, `"name":"edge","primary_managed_ap_locations":["Global/Lab/Building"]`, 1)
	wants, err := parseFabricEWLCIntents([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	deviceReads := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if len(intercept) > 0 && intercept[0](w, r) {
			return
		}
		if r.Method != "GET" {
			t.Error("preflight mutated appliance")
			w.WriteHeader(400)
			return
		}
		var rows any
		switch r.URL.Path {
		case devicesPath:
			deviceReads++
			devices := []Device{{ID: "edge-id", Hostname: "edge", Serial: "FOC12345678", ManagementIP: "198.51.100.101", Family: "Switches and Hubs", Reachability: "Reachable"}, {ID: "peer-id", Hostname: "peer", Serial: "FOC87654321", ManagementIP: "198.51.100.102", Family: "Switches and Hubs", Reachability: "Reachable"}}
			if mode == "changed-peer" && deviceReads > 1 {
				devices[1].Serial = "FOC99999999"
			}
			rows = devices
		case sitesPath:
			rows = sampleSites()
		case fabricSitesPath:
			b := false
			rows = []fabricSite{{ID: "fabric", SiteID: "area", Authentication: "Closed Authentication", PubSub: &b}}
		case wiredProvisionPath:
			provisioned := []wiredProvisionedDevice{{ID: "provisioned", DeviceID: "edge-id", SiteID: "building"}}
			if mode == "unprovisioned" {
				provisioned = []wiredProvisionedDevice{}
			}
			rows = provisioned
		case assignedDevicesPath:
			assignments := []map[string]string{{"deviceId": "edge-id", "siteId": "building"}}
			if mode == "unassigned" {
				assignments = []map[string]string{}
			}
			rows = assignments
		case fabricDevicesPath:
			if r.URL.Query().Get("fabricId") != "fabric" {
				t.Error("unscoped fabric read")
			}
			members := []fabricDeviceConfiguration{{ID: "edge-member", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}, {ID: "peer-member", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}}}
			if mode == "missing-membership" {
				members = members[1:]
			}
			if mode == "wrong-role" {
				members[0].Roles = []string{"CONTROL_PLANE_NODE"}
			}
			rows = members
		case "/dna/intent/api/v1/sda/fabrics/fabric/switchWirelessSetting":
			percentage := 25
			if mode == "late-settings" && deviceReads > 2 {
				percentage = 15
			}
			rows = []fabricEWLCSetting{{ID: "edge-id", Enabled: true, Rolling: fabricEWLCRolling{Percentage: percentage}}}
		case "/dna/intent/api/v1/wirelessControllers/edge-id/managedApLocations/count":
			n := 1
			if mode == "missing-placement" {
				n = 0
			}
			rows = map[string]int{"primaryManagedApLocationsCount": n, "secondaryManagedApLocationsCount": 0, "anchorManagedApLocationsCount": 0}
		case "/dna/intent/api/v1/wirelessControllers/edge-id/primaryManagedApLocations":
			locations := []wirelessManagedAPLocation{{SiteID: "building", SitePath: "Global/Lab/Building"}}
			if mode == "missing-placement" {
				locations = []wirelessManagedAPLocation{}
			}
			rows = []any{map[string]any{"managedApLocations": locations}}
		case "/dna/intent/api/v1/wirelessControllers/edge-id/secondaryManagedApLocations":
			rows = []any{map[string]any{"managedApLocations": []wirelessManagedAPLocation{}}}
		default:
			t.Error("unexpected endpoint", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"response": rows})
	})
	return c, wants[0]
}

func TestFabricEWLCComposedDependencies(t *testing.T) {
	for _, mode := range []string{"valid", "unprovisioned", "unassigned", "missing-membership", "wrong-role", "changed-peer", "missing-placement"} {
		t.Run(mode, func(t *testing.T) {
			c, want := ewlcComposedFixture(t, mode)
			got, err := c.fabricEWLCFabricDependencies(context.Background(), want)
			if mode != "valid" {
				if err == nil || got != nil {
					t.Fatal("partial prerequisites accepted", err)
				}
				return
			}
			if err != nil || len(got.foundation.physical) != 2 || got.target.Serial != "foc12345678" || got.locations.primary["Global/Lab/Building"] != "building" {
				t.Fatal(got, err)
			}
		})
	}
}

func TestFabricEWLCComposedManagedTargets(t *testing.T) {
	for _, mode := range []string{"valid", "late-settings"} {
		t.Run(mode, func(t *testing.T) {
			a, config, _, _, _ := portChannelJournalFixture(t, "valid")
			c, want := ewlcComposedFixture(t, mode)
			got, targets, err := c.fabricEWLCDependencyTargets(context.Background(), a.statusReader, config.Namespace, want)
			if mode != "valid" {
				if !errors.Is(err, errConfigurationStale) || got != nil || targets != nil {
					t.Fatal("late dependency change admitted")
				}
				return
			}
			if err != nil || len(targets) != 2 || len(got.foundation.physical) != 2 {
				t.Fatal(got, targets, err)
			}
		})
	}
}

func TestFabricEWLCExistingRoleDependencies(t *testing.T) {
	for _, mode := range []string{"valid", "unprovisioned", "unassigned", "missing-membership", "wrong-role", "changed-peer", "missing-placement"} {
		t.Run(mode, func(t *testing.T) {
			c, want := ewlcComposedFixture(t, mode)
			want.device.roles = nil
			got, err := c.fabricEWLCFabricDependencies(context.Background(), want)
			if mode != "valid" {
				if err == nil || got != nil {
					t.Fatal("unready inherited membership accepted", got, err)
				}
				return
			}
			if err != nil || got == nil || len(got.foundation.native.Roles) != 1 || got.foundation.native.Roles[0] != "EDGE_NODE" {
				t.Fatal("existing role not bound", got, err)
			}
			// The shared creation dependency path still requires explicit desired roles.
			if _, err := c.fabricDeviceDependencies(context.Background(), want.device); err == nil {
				t.Fatal("existing-role projection escaped into creation")
			}
		})
	}
}

func TestFabricEWLCExistingRolesCannotChangeAfterPlanning(t *testing.T) {
	var changed atomic.Bool
	c, want := ewlcComposedFixture(t, "valid", func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != fabricDevicesPath || !changed.Load() {
			return false
		}
		json.NewEncoder(w).Encode(map[string]any{"response": []fabricDeviceConfiguration{
			{ID: "edge-member", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"CONTROL_PLANE_NODE", "EDGE_NODE"}},
			{ID: "peer-member", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}},
		}})
		return true
	})
	want.device.roles = nil
	want.settings.rolling = true
	guardCalls := 0
	write, err := c.planGuardedFabricEWLC(context.Background(), want, func(context.Context) error { guardCalls++; return nil })
	if err != nil || write == nil {
		t.Fatal("plan", err)
	}
	changed.Store(true)
	if _, err := write.submit(context.Background()); !errors.Is(err, errConfigurationStale) {
		t.Fatal("changed inherited roles accepted", err)
	}
	if guardCalls != 0 {
		t.Fatal("stale roles reached mutation guard")
	}
	// An independent new observation may bind the new roles; it must retain both.
	deps, err := c.fabricEWLCFabricDependencies(context.Background(), want)
	if err != nil || deps == nil || len(deps.foundation.native.Roles) != 2 {
		t.Fatal("multiple inherited roles lost", deps, err)
	}
}

func TestFabricEWLCExistingBorderSettingsPreserved(t *testing.T) {
	c, want := ewlcComposedFixture(t, "valid", func(w http.ResponseWriter, r *http.Request) bool {
		if r.URL.Path != fabricDevicesPath {
			return false
		}
		json.NewEncoder(w).Encode(map[string]any{"response": []fabricDeviceConfiguration{
			{ID: "edge-member", NetworkDeviceID: "edge-id", FabricID: "fabric", Roles: []string{"BORDER_NODE", "EDGE_NODE"}, Border: &fabricBorderSettings{Types: []string{"LAYER_2"}}},
			{ID: "peer-member", NetworkDeviceID: "peer-id", FabricID: "fabric", Roles: []string{"EDGE_NODE"}},
		}})
		return true
	})
	want.device.roles = nil
	deps, err := c.fabricEWLCFabricDependencies(context.Background(), want)
	if err != nil || deps == nil {
		t.Fatal(err)
	}
	if deps.foundation.native.Border == nil || len(deps.foundation.native.Border.Types) != 1 || deps.foundation.native.Border.Types[0] != "LAYER_2" || len(deps.foundation.native.Roles) != 2 {
		t.Fatal("existing border configuration lost", deps.foundation.native)
	}
	if !fabricDeviceConfigurationMatches(deps.foundation.native, deps.foundation.before["edge-id"]) {
		t.Fatal("inherited settings propose base membership changes")
	}
}
