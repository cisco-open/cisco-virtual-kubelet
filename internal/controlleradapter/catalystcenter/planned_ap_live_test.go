// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type livePlannedAPTrace struct {
	base   http.RoundTripper
	t      *testing.T
	writes *atomic.Int64
}

func (trace livePlannedAPTrace) RoundTrip(req *http.Request) (*http.Response, error) {
	if trace.writes != nil && req.Method != http.MethodGet && req.URL.Path != authPath {
		trace.writes.Add(1)
	}
	response, err := trace.base.RoundTrip(req)
	if req.Method != http.MethodGet && strings.Contains(req.URL.Path, "/plannedAccessPointPositions/") {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		trace.t.Log("planned AP native response", req.Method, "HTTP", status, "transportError", err != nil)
		if response != nil && status == http.StatusBadRequest {
			// Only this isolated, nonsecret map-design request is traced.
			// Authentication responses and headers are never inspected.
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 8193))
			response.Body = struct {
				io.Reader
				io.Closer
			}{io.MultiReader(bytes.NewReader(body), response.Body), response.Body}
			if readErr == nil && len(body) <= 8192 {
				trace.t.Log("planned AP validation response", string(body))
			}
		}
	}
	return response, err
}

// Only map-design objects on the enclosing harness's owned floor are touched.
// No operational AP assignment or provisioning endpoint is used.
func qualifyLivePlannedAPManifest(t *testing.T, ctx context.Context, h *liveSiteManifest, owned map[string]Site, wait func(context.Context, Task, time.Time) error, settled *bool) {
	t.Helper()
	if h == nil || h.config.Spec.Source.Inline == nil {
		t.Fatal("planned AP qualification requires the isolated inline manifest")
	}
	var root map[string]any
	if json.Unmarshal(h.config.Spec.Source.Inline.Raw, &root) != nil {
		t.Fatal("isolated source")
	}
	floors := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)
	if len(floors) != 1 {
		t.Fatal("isolated floor scope")
	}
	floor := floors[0].(map[string]any)
	floorPath := floor["parent_name"].(string) + "/" + floor["name"].(string)
	floorID := ""
	for id, site := range owned {
		if site.Type == "floor" && site.NameHierarchy == floorPath {
			floorID = id
		}
	}
	if !validAPIID(floorID) {
		t.Fatal("isolated floor ownership missing")
	}
	c := h.a.client
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	base := rest.HTTPClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	mutations := &atomic.Int64{}
	rest.HTTPClient.Transport = livePlannedAPTrace{base: base, t: t, writes: mutations}
	baseline, err := c.plannedAPPositions(ctx, floorID)
	if err != nil || len(baseline) != 0 {
		t.Fatal("owned floor is not empty or readable", err)
	}
	name := "CVK-Planned-AP"
	id := ""
	var expected plannedAPObservation
	check := func(checkCtx context.Context) {
		t.Helper()
		sites, err := c.ListSites(checkCtx)
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, site := range sites {
			if site.ID == floorID || site.NameHierarchy == floorPath {
				if site.ID != floorID || site.NameHierarchy != floorPath || site.Type != "floor" {
					t.Fatal("owned floor identity changed; retain")
				}
				found = true
			}
		}
		if !found {
			t.Fatal("owned floor disappeared; retain")
		}
		positions, err := c.plannedAPPositions(checkCtx, floorID)
		if err != nil || len(positions) != 1 || !reflect.DeepEqual(positions[name], expected) || positions[name].ID != id {
			t.Fatal("planned AP scope or identity changed; retain", err)
		}
	}
	// Registered after hierarchy cleanup: this exact AP is removed first. An
	// uncertain write or changed scope leaves the entire hierarchy retained.
	t.Cleanup(func() {
		if !*settled || id == "" {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		*settled = false
		check(cleanupCtx)
		submitted := time.Now()
		raw, err := c.mutate(cleanupCtx, http.MethodDelete, plannedAPPositionsPath(floorID)+"/"+id, nil)
		if err != nil {
			t.Error("planned AP cleanup uncertain; no replay", err)
			return
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Error("planned AP cleanup missing receipt; no replay")
			return
		}
		if err := wait(cleanupCtx, receipt.Response, submitted); err != nil {
			t.Error("planned AP cleanup task", err)
			return
		}
		positions, err := c.plannedAPPositions(cleanupCtx, floorID)
		if err != nil || !reflect.DeepEqual(positions, baseline) {
			t.Error("planned AP cleanup did not restore baseline", err)
			return
		}
		*settled = true
		t.Log("exact planned AP UUID cleanup and empty-floor baseline verified")
	})
	entry := map[string]any{"name": name, "ap_type": "AP9130I", "position_x": 4.5, "position_y": 6.0, "position_z": 4.0, "mac_address": "02:00:00:00:00:01", "radios": []any{map[string]any{"band": 5.0, "channel": 36.0, "tx_power": 12.0, "antenna_name": "Internal-9130-5GHz", "antenna_azimuth": 90.0, "antenna_elevation": -10.0}}}
	floor["planned_access_points"] = []any{entry}
	for _, axis := range []string{"x", "y", "z"} {
		key := "position_" + axis
		original := entry[key]
		entry[key] = 1000.0
		raw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		executor, err := c.compileConfigurationExecutor(raw, "sites")
		if err != nil {
			t.Fatal("bounds source compile", err)
		}
		before := mutations.Load()
		write, err := executor.next(ctx)
		entry[key] = original
		if err == nil || write != nil || mutations.Load() != before {
			t.Fatal("invalid native position escaped complete-source preflight", axis, err)
		}
		t.Log("native floor bounds preflight refused invalid coordinate without mutation", axis)
	}
	for revision := 0; revision < 2; revision++ {
		if revision > 0 {
			check(ctx)
			entry["position_x"] = 5.5
		}
		raw, err := json.Marshal(root)
		if err != nil {
			t.Fatal(err)
		}
		_, intents, err := parseSitesWithPlannedAPs(raw)
		if err != nil || len(intents) != 1 || intents[0].floor != floorPath {
			t.Fatal("planned AP source scope", err)
		}
		if err := h.a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(h.config), h.config); err != nil {
			t.Fatal(err)
		}
		h.config.Generation++
		h.config.Spec.Source.Inline.Raw = raw
		if err := h.a.statusWriter.Update(ctx, h.config); err != nil {
			t.Fatal(err)
		}
		action := "Create"
		if revision > 0 {
			action = "Update"
		}
		*settled = false
		task, _ := h.run(t, ctx, plannedAPResource(intents[0]), action)
		positions, err := c.plannedAPPositions(ctx, floorID)
		if err != nil || len(positions) != 1 || !validAPIID(positions[name].ID) {
			t.Fatal("planned AP result scope; retain", err)
		}
		if id != "" && id != positions[name].ID {
			t.Fatal("planned AP UUID changed during update; retain")
		}
		if revision > 0 {
			previous := expected
			position := *expected.Position
			x := entry["position_x"].(float64)
			position.X = &x
			previous.Position = &position
			if !reflect.DeepEqual(previous, positions[name]) {
				t.Fatal("position update changed unselected planned AP/radio state; retain")
			}
		}
		id, expected = positions[name].ID, positions[name]
		*settled = true
		check(ctx)
		if os.Getenv("CATC_PLANNED_AP_TASK_RESULT") == "isolated-result-inspection" {
			raw, err := c.get(ctx, taskPath+task.ID, nil, http.StatusOK)
			if err != nil {
				t.Fatal("planned AP result inspection failed", err)
			}
			var body struct {
				Response struct {
					Data string `json:"data"`
				} `json:"response"`
			}
			if json.Unmarshal(raw, &body) != nil {
				t.Fatal("planned AP task response invalid")
			}
			var object map[string]json.RawMessage
			var array []json.RawMessage
			objectOK := json.Unmarshal([]byte(body.Response.Data), &object) == nil && object != nil
			arrayOK := json.Unmarshal([]byte(body.Response.Data), &array) == nil && array != nil
			keys := []string{}
			for key := range object {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			t.Log("planned AP task result metadata", "action", action, "task", task.ID, "AP UUID", id, "plainAPMatch", body.Response.Data == id, "plainFloorMatch", body.Response.Data == floorID, "object", objectOK, "array", arrayOK, "keys", keys, "arrayLength", len(array))
		}
		t.Log("planned AP public manifest task/readback/restart/no-op verified", "revision", revision+1, "radioCount", len(expected.Radios))
	}
}
