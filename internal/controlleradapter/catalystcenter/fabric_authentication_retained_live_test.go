// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// An explicitly bound retained empty fixture may be audited, used for a new
// No Authentication intent, or cleaned. Never replay the original Closed
// Authentication request and never identify deletion targets by name prefix.
func TestLiveRetainedEmptyFabricQualification(t *testing.T) {
	mode := os.Getenv("CATC_EMPTY_FABRIC_RETAINED_MODE")
	if mode != "new-no-auth-intent" && mode != "wired-no-auth-intent" && mode != "cleanup-area" {
		t.Skip("exact retained fixture action not enabled")
	}
	id, path, taskID := os.Getenv("CATC_EMPTY_FABRIC_SITE_ID"), os.Getenv("CATC_EMPTY_FABRIC_SITE_PATH"), os.Getenv("CATC_EMPTY_FABRIC_AREA_TASK")
	baselinePath := os.Getenv("CATC_EMPTY_FABRIC_GLOBAL_BASELINE")
	if !validAPIID(id) || !validAPIID(taskID) || !validSitePath(path) || !strings.HasPrefix(path, "Global/CVK-Q-Auth-") || !strings.HasPrefix(baselinePath, "/private/tmp/cvk-auth-template-baseline-") || !strings.HasSuffix(baselinePath, "/baseline.json") {
		t.Fatal("exact retained area, task and private baseline binding required")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	var global map[string]map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&global) != nil || len(global) != 3 {
		t.Fatal("private global baseline invalid")
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	baselineSites := []Site{}
	found := false
	for _, s := range sites {
		if s.ID == id {
			if s.NameHierarchy != path || s.Type != "area" {
				t.Fatal("retained fixture moved or replaced")
			}
			found = true
		} else {
			baselineSites = append(baselineSites, s)
		}
	}
	if !found {
		t.Fatal("exact retained area missing")
	}
	sort.Slice(baselineSites, func(i, j int) bool { return baselineSites[i].ID < baselineSites[j].ID })
	devices, err := c.configurationDevices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
	raw, err = c.get(ctx, taskPath+taskID, nil, http.StatusOK)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Response struct {
			ID    string `json:"id"`
			Start int64  `json:"startTime"`
			End   int64  `json:"endTime"`
			Error *bool  `json:"isError"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &created) != nil || created.Response.ID != taskID || created.Response.Start <= 0 || created.Response.End < created.Response.Start || created.Response.Error == nil || *created.Response.Error {
		t.Fatal("recorded area task not successful")
	}
	started := time.UnixMilli(created.Response.Start)
	if complete, err := c.configurationTaskTreeComplete(ctx, taskID, started, time.Now()); err != nil || !complete {
		t.Fatal("area task tree not complete", err)
	}
	windowEnd := created.Response.Start + 60000
	if time.Now().UnixMilli() <= windowEnd {
		t.Fatal("recorded request window not yet closed")
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	type taskMetadata struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		End    int64  `json:"endTime"`
	}
	tasks, err := listInventoryQueryWithGet(ctx, get, "/dna/intent/api/v1/tasks", url.Values{"startTime": {strconv.FormatInt(created.Response.Start-5000, 10)}, "endTime": {strconv.FormatInt(windowEnd, 10)}}, func(task taskMetadata) string { return task.ID })
	if err != nil {
		t.Fatal("request window audit incomplete", err)
	}
	for _, task := range tasks {
		if task.End <= 0 || (task.Status != "SUCCESS" && task.Status != "FAILURE") {
			t.Fatal("recorded window contains a nonterminal task", task.ID)
		}
	}
	if mode != "new-no-auth-intent" {
		failedID := os.Getenv("CATC_EMPTY_FABRIC_FAILED_TASK")
		if !validAPIID(failedID) {
			t.Fatal("known failed fabric task binding required")
		}
		verifyLiveEmptyFabricFailedTree(t, ctx, c, failedID, started)
	}
	t.Log("recorded area task and closed request window verified", taskID, "terminal records", len(tasks))
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	h := &liveEmptyFabricAuthTrace{base: base, client: c, sites: baselineSites, devices: devices, global: global, path: path, siteID: id, profileName: "No Authentication"}
	rest.HTTPClient.Transport = h
	if err := h.scope(ctx); err != nil {
		t.Fatal("retained scope is no longer empty and unchanged", err)
	}
	for _, kind := range []string{"telemetry", "aaa"} {
		observed, err := c.readNetworkSetting(ctx, id, kind)
		t.Log("retained area prerequisite metadata", kind, "readable", err == nil, "telemetry groups", len(observed.telemetry), "AAA groups", len(observed.aaa))
	}
	mutate := func(method, endpoint string, body any) {
		t.Helper()
		h.arm(method, endpoint, body)
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		submitted := time.Now()
		reply, err := c.mutate(ctx, method, endpoint, raw)
		if err != nil {
			t.Log("private failure response", h.failureDiagnostic)
			t.Fatal("retained-scope operation uncertain; inspect before further write", err)
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(reply, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Fatal("retained-scope task receipt missing")
		}
		t.Log("retained-scope operation", method, endpoint, receipt.Response.ID)
		if err := waitLiveEmptyFabricAuthTask(ctx, c, receipt.Response, submitted); err != nil {
			t.Fatal(err)
		}
	}
	if mode == "wired-no-auth-intent" {
		enableLiveEmptyFabricWiredCollection(t, ctx, c, h, mutate)
	}
	if mode == "new-no-auth-intent" || mode == "wired-no-auth-intent" {
		b := false
		mutate(http.MethodPost, fabricSitesPath, []fabricSite{{SiteID: id, Authentication: "No Authentication", PubSub: &b}})
		fabrics, err := c.fabricSites(ctx)
		if err != nil || len(fabrics) != 1 || !validAPIID(fabrics[id].ID) {
			t.Fatal("new No Authentication fabric identity missing", err)
		}
		h.fabricID = fabrics[id].ID
		t.Log("captured owned No Authentication fabric", h.siteID, h.fabricID)
		if err := h.scope(ctx); err != nil {
			t.Fatal(err)
		}
		profiles, err := c.listAuthProfiles(ctx, h.fabricID)
		if err != nil {
			t.Fatal("scoped profile inventory failed", err)
		}
		t.Log("new No Authentication fabric accepted", h.fabricID, "native profiles", len(profiles))
		mutate(http.MethodDelete, fabricSitesPath+"/"+url.PathEscape(h.fabricID), nil)
		fabrics, err = c.fabricSites(ctx)
		if err != nil || len(fabrics) != 0 {
			t.Fatal("owned No Authentication fabric remains", err)
		}
		h.fabricID = ""
	}
	mutate(http.MethodDelete, "/dna/intent/api/v1/areas/"+url.PathEscape(id), nil)
	h.siteID = ""
	if err := h.scope(ctx); err != nil {
		t.Fatal("retained area cleanup not verified", err)
	}
	after, err := liveRawAuthCatalog(ctx, c)
	if err != nil || !reflect.DeepEqual(after, global) {
		t.Fatal("global baseline changed", err)
	}
	t.Log("exact retained empty area removed", id, "global, hierarchy and device baselines unchanged")
}

// Terminal failure alone never authorizes device cleanup. This diagnostic is
// limited to an independently proved empty, test-owned controller definition.
// Validate the entire rooted tree rather than stopping at its first error row.
func verifyLiveEmptyFabricFailedTree(t *testing.T, ctx context.Context, c *client, id string, earliest time.Time) {
	t.Helper()
	raw, err := c.get(ctx, taskPath+id+"/tree", nil, http.StatusOK)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Response []struct {
			ID     string `json:"id"`
			Parent string `json:"parentId"`
			Root   string `json:"rootId"`
			Code   string `json:"errorCode"`
			Start  *int64 `json:"startTime"`
			End    *int64 `json:"endTime"`
			Error  *bool  `json:"isError"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &response) != nil || len(response.Response) == 0 || len(response.Response) > 10000 {
		t.Fatal("failed task tree incomplete")
	}
	parents := map[string]string{}
	failedRoot := false
	for _, row := range response.Response {
		if !validAPIID(row.ID) || row.Start == nil || row.End == nil || row.Error == nil || *row.End < *row.Start || *row.Start < earliest.Add(-configurationTaskClockSkew).UnixMilli() || *row.End > time.Now().Add(configurationTaskClockSkew).UnixMilli() {
			t.Fatal("failed task tree has incomplete or uncorrelated terminal metadata")
		}
		if _, duplicate := parents[row.ID]; duplicate {
			t.Fatal("duplicate failed task identity")
		}
		if row.ID == id {
			if row.Parent != "" || (row.Root != "" && row.Root != id) || !*row.Error || row.Code != "NCSP11008" {
				t.Fatal("failed task root is not the diagnosed application lookup failure")
			}
			failedRoot = true
		} else if row.Root != id || !validAPIID(row.Parent) {
			t.Fatal("failed task tree disconnected")
		}
		parents[row.ID] = row.Parent
	}
	if !failedRoot {
		t.Fatal("failed task root missing")
	}
	for node := range parents {
		seen := map[string]bool{}
		for node != id {
			parent, ok := parents[node]
			if !ok || seen[node] {
				t.Fatal("failed task parent chain disconnected or cyclic")
			}
			seen[node] = true
			node = parent
		}
	}
	t.Log("diagnosed failed task and every descendant are terminal", id, "nodes", len(parents))
}

// Observe an already acknowledged deletion after a transient incomplete tree.
// This test has no mutation call and cannot replay DELETE.
func TestLiveRetainedEmptyFabricCleanupReadback(t *testing.T) {
	if os.Getenv("CATC_EMPTY_FABRIC_RETAINED_MODE") != "observe-area-cleanup" {
		t.Skip("exact cleanup observation not enabled")
	}
	id, path, taskID := os.Getenv("CATC_EMPTY_FABRIC_SITE_ID"), os.Getenv("CATC_EMPTY_FABRIC_SITE_PATH"), os.Getenv("CATC_EMPTY_FABRIC_DELETE_TASK")
	baselinePath := os.Getenv("CATC_EMPTY_FABRIC_GLOBAL_BASELINE")
	if !validAPIID(id) || !validAPIID(taskID) || !validSitePath(path) || !strings.HasPrefix(path, "Global/CVK-Q-Auth-") || !strings.HasPrefix(baselinePath, "/private/tmp/cvk-auth-template-baseline-") || !strings.HasSuffix(baselinePath, "/baseline.json") {
		t.Fatal("exact acknowledged cleanup binding missing")
	}
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	raw, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	var baseline map[string]map[string]any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&baseline) != nil {
		t.Fatal("private baseline invalid")
	}
	raw, err = c.get(ctx, taskPath+taskID, nil, http.StatusOK)
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		Response struct {
			ID    string `json:"id"`
			Start int64  `json:"startTime"`
			End   int64  `json:"endTime"`
			Error *bool  `json:"isError"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &task) != nil || task.Response.ID != taskID || task.Response.Error == nil || *task.Response.Error || task.Response.Start <= 0 || task.Response.End < task.Response.Start {
		t.Fatal("acknowledged delete root not successful")
	}
	var inventory []Device
	for range 2 {
		if done, err := c.configurationTaskTreeComplete(ctx, taskID, time.UnixMilli(task.Response.Start), time.Now()); err != nil || !done {
			t.Fatal("exact deletion tree not successfully complete", err)
		}
		sites, err := c.ListSites(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range sites {
			if s.ID == id || s.NameHierarchy == path || strings.HasPrefix(s.NameHierarchy, path+"/") {
				t.Fatal("owned area or descendant still exists")
			}
		}
		fabrics, err := c.fabricSites(ctx)
		if err != nil || len(fabrics) != 0 {
			t.Fatal("native fabric cleanup not empty", err)
		}
		zones, err := c.fabricZones(ctx)
		if err != nil || len(zones) != 0 {
			t.Fatal("native zone baseline changed", err)
		}
		global, err := liveRawAuthCatalog(ctx, c)
		if err != nil || !reflect.DeepEqual(global, baseline) {
			t.Fatal("original global authentication baseline changed", err)
		}
		devices, err := c.configurationDevices(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sort.Slice(devices, func(i, j int) bool { return devices[i].ID < devices[j].ID })
		if inventory != nil && !reflect.DeepEqual(inventory, devices) {
			t.Fatal("device inventory changed during cleanup observation")
		}
		inventory = devices
	}
	t.Log("acknowledged delete and full task tree verified twice without replay", taskID)
	t.Log("exact empty area and descendants absent; zero fabrics/zones; original global authentication baseline unchanged", id, "devices", len(inventory))
}
