// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestModernSWIMWireContract(t *testing.T) {
	calls := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == authPath {
			_, _ = w.Write([]byte(`{"Token":"test-token"}`))
			return
		}
		calls++
		switch r.URL.Path {
		case networkDeviceImagesPath + "device/distribute", networkDeviceImagesPath + "device/activate":
			field := "distributedImages"
			if r.URL.Path == networkDeviceImagesPath+"device/activate" {
				field = "installedImages"
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if r.Method != "POST" || !reflect.DeepEqual(body, map[string]any{field: []any{map[string]any{"id": "image"}}}) {
				t.Errorf("unexpected request: %s %+v", r.Method, body)
			}
			_, _ = w.Write([]byte(`{"response":{"taskId":"task"}}`))
		case imageUpdatesPath:
			if r.Method != "GET" || r.URL.Query().Get("parentId") != "task" || r.URL.Query().Get("networkDeviceId") != "device" {
				t.Error("unbound workflow query")
			}
			_, _ = w.Write([]byte(`{"response":[{"id":"update","parentId":"task","networkDeviceId":"device"}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	for _, name := range []string{"username", "password"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := newClient(clientConfig{Endpoint: srv.URL, CredentialPath: dir, InsecureSkipVerify: true, RequestTimeout: time.Second})
	ctx := context.Background()
	if _, err := c.DistributeImages(ctx, "device", "image"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateImages(ctx, "device", "image"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ListImageUpdates(ctx, "device", "task"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdateImages(ctx, "../other", "image"); err == nil {
		t.Fatal("accepted path injection")
	}
	if calls != 3 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestModernWorkflowRequiresCorrelatedCompletion(t *testing.T) {
	now := time.Now()
	submitted := now.Add(-time.Minute)
	base := ImageUpdate{ID: "update", ParentID: "task", DeviceID: "device", Version: "17.18.04.0.759", Type: "DISTRIBUTE", Status: "SUCCESS", StartTime: submitted.Add(time.Second).UnixMilli(), EndTime: now.Add(-time.Second).UnixMilli()}
	check := func(items []ImageUpdate) (swimTaskState, error) {
		return imageUpdateState(items, "device", "task", base.Version, "DISTRIBUTE", submitted, now)
	}
	if s, err := check([]ImageUpdate{base}); err != nil || !s.complete {
		t.Fatalf("state=%+v err=%v", s, err)
	}
	if s, err := check(nil); err != nil || s.complete {
		t.Fatal("empty workflow completed")
	}
	if _, err := check([]ImageUpdate{base, base}); err == nil {
		t.Fatal("duplicate workflows accepted")
	}
	for name, mutate := range map[string]func(*ImageUpdate){
		"wrong device": func(i *ImageUpdate) { i.DeviceID = "other" }, "wrong task": func(i *ImageUpdate) { i.ParentID = "other" },
		"wrong image": func(i *ImageUpdate) { i.Version = "17.18.02" }, "wrong operation": func(i *ImageUpdate) { i.Type = "ACTIVATE" },
		"old workflow": func(i *ImageUpdate) { i.StartTime = submitted.Add(-time.Hour).UnixMilli() },
		"future end":   func(i *ImageUpdate) { i.EndTime = now.Add(time.Hour).UnixMilli() },
		"missing end":  func(i *ImageUpdate) { i.EndTime = 0 }, "unknown": func(i *ImageUpdate) { i.Status = "COMPLETE" },
	} {
		t.Run(name, func(t *testing.T) {
			i := base
			mutate(&i)
			if _, err := check([]ImageUpdate{i}); err == nil {
				t.Fatal("accepted invalid workflow")
			}
		})
	}
}

// The appliance's child workflow uses DISTRIBUTE, not DISTRIBUTION.
func TestModernLiveDistributionWireShape(t *testing.T) {
	var body struct {
		Response []ImageUpdate `json:"response"`
	}
	raw := `{"response":[{"id":"01a11c2b-adef-73d4-9b06-02d3ad2c1014","parentId":"01a11c2b-9ff7-7a65-aa15-3d7734f696e1","networkDeviceId":"70601ab8-5104-46bc-a061-1ffbaf108c16","updateImageVersion":"17.18.04.0.759","type":"DISTRIBUTE","startTime":1791473987553,"endTime":0,"status":"IN_PROGRESS"}]}`
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatal(err)
	}
	item := body.Response[0]
	submitted := time.UnixMilli(item.StartTime - 1000)
	now := time.UnixMilli(item.StartTime + 1000)
	state, err := imageUpdateState(body.Response, item.DeviceID, item.ParentID, item.Version, "DISTRIBUTE", submitted, now)
	if err != nil || state.complete || state.failed {
		t.Fatalf("live in-progress response: %+v %v", state, err)
	}
	body.Response[0].Type = "DISTRIBUTION"
	if _, err := imageUpdateState(body.Response, item.DeviceID, item.ParentID, item.Version, "DISTRIBUTE", submitted, now); err == nil {
		t.Fatal("unqualified workflow enum accepted")
	}
}
