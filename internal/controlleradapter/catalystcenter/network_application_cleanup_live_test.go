// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Exact retained fixtures from application and optional-DNS qualifications.
// Correlate original creator tasks and unchanged native footprints before any
// deletion. This is supervised test cleanup, not public destructive lifecycle.
func TestLiveNetworkApplicationRetainedCleanup(t *testing.T) {
	telemetry := os.Getenv("CATC_TELEMETRY_PARTIAL_CLEANUP") == "20261017-041545"
	dnsRun := os.Getenv("CATC_DNS_OPTIONAL_CLEANUP")
	dns := dnsRun == "20261017-034335" || dnsRun == "20261017-034659"
	if !telemetry && !dns && os.Getenv("CATC_NETWORK_APPLICATION_CLEANUP") != "20261017-030846" {
		t.Skip("exact retained application fixture not selected")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	root := "Global/CVK-Q-20261017-030846-000000000"
	if dns {
		root = "Global/CVK-Q-20261017-034335-000000000"
	}
	if dnsRun == "20261017-034659" {
		root = "Global/CVK-Q-20261017-034659-000000000"
	}
	if telemetry {
		root = "Global/CVK-Q-20261017-041545-000000000"
	}
	paths := []string{root, root + "/TestBuilding", root + "/TestBuilding/TestFloor"}
	createTasks := []string{"01a147d5-5aec-7141-8a28-d619e336a884", "01a147d5-61d5-74a0-9bb9-39e6543c61db", "01a147d5-6479-76aa-800e-1297f1831503"}
	submitted := time.Date(2026, 10, 17, 3, 8, 46, 0, time.UTC)
	if dns {
		createTasks = []string{"01a147f5-3a57-770c-9169-01b0673840b2", "01a147f5-3cd5-73c4-92c6-05c5cbff341a", "01a147f5-3f7b-732c-8949-df4d32330ea6"}
		submitted = time.Date(2026, 10, 17, 3, 43, 35, 0, time.UTC)
	}
	if dnsRun == "20261017-034659" {
		createTasks = []string{"01a147f8-5978-7231-929c-fda93b349896", "01a147f8-5bf5-7df4-bba2-c235922e4d8a", "01a147f8-5e89-7cb9-aad1-a2b29ea82c43"}
		submitted = time.Date(2026, 10, 17, 3, 47, 0, 0, time.UTC)
	}
	if telemetry {
		createTasks = []string{"01a14812-af88-7373-97a8-8705c4e8cefd", "01a14812-b64c-7120-996f-f4325684343a", "01a14812-b8f3-75ee-aeeb-ef922d51bc2b"}
		submitted = time.Date(2026, 10, 17, 4, 15, 46, 0, time.UTC)
	}
	current, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, site := range current {
		if site.NameHierarchy == root || strings.HasPrefix(site.NameHierarchy, root+"/") {
			found = true
		}
	}
	if !found {
		t.Log("exact retained hierarchy already absent; zero cleanup mutations")
		return
	}
	ids := map[string]string{}
	for i, task := range createTasks {
		if done, err := c.configurationTaskTreeComplete(ctx, task, submitted, time.Now()); err != nil || !done {
			t.Fatal("original create task incomplete", err)
		}
		raw, err := c.get(ctx, taskPath+task, nil, http.StatusOK)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			Response struct {
				Data string `json:"data"`
			} `json:"response"`
		}
		if json.Unmarshal(raw, &result) != nil || !canonicalTemplateUUID(result.Response.Data) {
			t.Fatal("original creator UUID unavailable")
		}
		ids[paths[i]] = result.Response.Data
	}
	model := fmt.Sprintf(`catalyst_center:
  sites:
    areas: [{name: %s, parent_name: Global}]
    buildings: [{name: TestBuilding, parent_name: %s, country: United States, latitude: 37.3381234, longitude: -121.8861234}]
    floors: [{name: TestFloor, parent_name: %s/TestBuilding, floor_number: 1, rf_model: Free Space, width: 12.654321, length: 15.123456, height: 3.123456, units_of_measure: meters}]
`, lastSiteName(root), root, root)
	desired, err := parseSites([]byte(model))
	if err != nil {
		t.Fatal(err)
	}
	if drift, err := planSites(desired, current); err != nil || len(drift) != 0 {
		t.Fatal("retained geometry changed; cleanup refused", err)
	}
	if telemetry {
		// The direct probe completed tasks but intentionally exposed a native
		// representation our strict typed reader cannot attest. This is exact
		// supervised cleanup, not public convergence, rollback or adoption.
		path := sitesPath + "/" + ids[root] + "/telemetrySettings"
		before, err := c.get(ctx, path, nil, http.StatusOK)
		if err != nil {
			t.Fatal(err)
		}
		after, err := c.get(ctx, path, nil, http.StatusOK)
		if err != nil || !bytes.Equal(before, after) {
			t.Fatal("unstable generated telemetry footprint; cleanup refused", err)
		}
	} else if dns {
		got, err := c.readNetworkSetting(ctx, ids[root], "dns")
		domain := "optional.example.test"
		servers := []string{}
		if dnsRun == "20261017-034659" {
			domain = "initial.example.test"
			servers = []string{"192.0.2.13", "192.0.2.14"}
		}
		if err != nil || got.inherited || got.unset || got.domain != domain || !reflect.DeepEqual(got.servers, servers) {
			t.Fatal("retained DNS footprint changed; cleanup refused", err)
		}
	} else {
		executor, err := c.compileConfigurationExecutor(liveNetworkSettingsModel(t, root), "network_settings")
		if err != nil {
			t.Fatal(err)
		}
		if next, err := executor.next(ctx); err != nil || next != nil {
			t.Fatal("retained setting footprint changed; cleanup refused", err)
		}
	}
	for i := len(paths) - 1; i >= 0; i-- {
		sites, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var selected *Site
		count := 0
		for _, site := range sites {
			if site.NameHierarchy == root || strings.HasPrefix(site.NameHierarchy, root+"/") {
				count++
				if ids[site.NameHierarchy] != site.ID {
					t.Fatal("foreign, replaced or moved hierarchy member; cleanup refused")
				}
				if assigned, err := c.assignedDevices(ctx, site.ID); err != nil || len(assigned) != 0 {
					t.Fatal("nonempty assignment population; cleanup refused", err)
				}
			}
			if site.NameHierarchy == paths[i] {
				copy := site
				selected = &copy
			}
		}
		if selected == nil || selected.ID != ids[paths[i]] || count != i+1 {
			t.Fatal("exact remaining hierarchy changed")
		}
		endpoint := map[string]string{"area": "/dna/intent/api/v1/areas/", "building": "/dna/intent/api/v2/buildings/", "floor": "/dna/intent/api/v2/floors/"}[selected.Type]
		if endpoint == "" {
			t.Fatal("unsupported generated member")
		}
		started := time.Now()
		raw, err := c.mutate(ctx, http.MethodDelete, endpoint+selected.ID, nil)
		if err != nil {
			t.Fatal("cleanup outcome uncertain; no replay", err)
		}
		var result struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &result) != nil || !validAPIID(result.Response.ID) {
			t.Fatal("cleanup acknowledgement uncertain; no replay")
		}
		t.Log("exact generated cleanup task", result.Response.ID)
		if err := waitSiteCredentialTask(ctx, c, result.Response, started); err != nil {
			t.Fatal(err)
		}
		if done, err := c.configurationTaskTreeComplete(ctx, result.Response.ID, started, time.Now()); err != nil || !done {
			t.Fatal("cleanup child task incomplete", err)
		}
		after, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, site := range after {
			if site.ID == selected.ID || site.NameHierarchy == paths[i] {
				t.Fatal("cleanup absence not verified")
			}
		}
	}
	t.Log("retained application hierarchy removed by exact original creator UUIDs; unchanged geometry/settings, empty assignments, complete delete tasks and final absence verified")
}
