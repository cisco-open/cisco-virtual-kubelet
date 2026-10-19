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
	"testing"
	"time"
)

func TestStandardReloadWarningIsBoundAndOptIn(t *testing.T) {
	for _, target := range []string{"26.02.01", "17.18.04"} {
		t.Run(target, func(t *testing.T) {
			now := time.Now()
			start := now.Add(-time.Minute)
			items := make([]ReadinessResult, len(requiredXEReadinessChecks))
			for n, name := range requiredXEReadinessChecks {
				items[n] = ReadinessResult{ID: name[:1] + "-id", ParentID: "task", DeviceID: "device", Operation: "READINESS_CHECK", Type: "PRE_VALIDATION", Name: name, Status: "SUCCESS", StartTime: start.UnixMilli(), EndTime: now.UnixMilli()}
				items[n].ID = string(rune('a' + n))
			}
			last := len(items) - 1
			items[last].Status = "WARNING"
			items[last].XFSUVersionPathTarget = target
			i := swimIntent{StandardReloadProfile: "CatalystCenter323", APIContract: swimModernContract, ControllerDeviceID: "device", TargetVersion: target}
			if err := validateStandardReloadReadiness(items, i, "task", start, now); err != nil {
				t.Fatal(err)
			}
			if items[last].Status != "WARNING" {
				t.Fatal("modified original evidence")
			}
			if err := validateXEReadiness(items, "device", "task", "", start, now); err == nil {
				t.Fatal("legacy profile accepted warning")
			}
			for _, change := range []func([]ReadinessResult){
				func(x []ReadinessResult) { x[last].XFSUVersionPathTarget = "26.02.02" },
				func(x []ReadinessResult) { x[last].XFSUVersionPathTarget = "" },
				func(x []ReadinessResult) { x[last].Status = "FAILURE" },
				func(x []ReadinessResult) { x[last].DeviceID = "other" },
				func(x []ReadinessResult) { x[last].ParentID = "other" },
				func(x []ReadinessResult) { x[last].StartTime = start.Add(-time.Hour).UnixMilli() },
				func(x []ReadinessResult) { x[0].Status = "WARNING" },
			} {
				x := append([]ReadinessResult(nil), items...)
				change(x)
				if err := validateStandardReloadReadiness(x, i, "task", start, now); err == nil {
					t.Fatal("accepted unqualified readiness")
				}
			}
			i.StandardReloadProfile = ""
			if err := validateStandardReloadReadiness(items, i, "task", start, now); err == nil {
				t.Fatal("accepted unpinned mode")
			}
		})
	}
}

func TestXFSUWarningParserDoesNotTrustTopLevelOrUnknownDetails(t *testing.T) {
	expected := "If upgrade image version is greater than 26.1.x, the running image version also must be 26.1.x or higher."
	description := "Upgrades using xfsu to 26.02.01 is not supported from running image version 17.18.4"
	for _, tc := range []struct{ expected, description, want string }{
		{expected, description, "26.02.01"}, {"", description, ""}, {expected, "FPGA incompatible", ""}, {expected, description + " extra", ""},
	} {
		body, _ := json.Marshal(map[string]any{"XFSUVersionPathTarget": "26.02.01", "resultDetails": []map[string]string{{"key": "EXPECTED", "value": tc.expected}, {"key": "DESCRIPTION", "value": tc.description}, {"key": "STATUS", "value": "WARNING"}}})
		var r ReadinessResult
		if err := json.Unmarshal(body, &r); err != nil {
			t.Fatal(err)
		}
		if r.XFSUVersionPathTarget != tc.want {
			t.Fatalf("got %q want %q", r.XFSUVersionPathTarget, tc.want)
		}
	}
}

func TestStandardReloadWireAndReleaseFence(t *testing.T) {
	version := "3.2.3-75346.100"
	posts := 0
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case authPath:
			_, _ = w.Write([]byte(`{"Token":"test"}`))
		case "/dna/intent/api/v1/dnac-release":
			_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]string{"installedVersion": version}})
		case networkDeviceImagesPath + "device/activate":
			posts++
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if r.Method != "POST" || string(body["compatibleFeatures"]) != "[]" || string(body["installedImages"]) != `[{"id":"image"}]` || len(body) != 2 {
				t.Errorf("unexpected activation: %s %+v", r.Method, body)
			}
			_, _ = w.Write([]byte(`{"response":{"taskId":"task"}}`))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	dir := t.TempDir()
	for _, n := range []string{"username", "password"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("test"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := newClient(clientConfig{Endpoint: srv.URL, CredentialPath: dir, InsecureSkipVerify: true, RequestTimeout: time.Second})
	if _, err := c.UpdateImagesStandardReload(context.Background(), "device", "image"); err != nil {
		t.Fatal(err)
	}
	version = "3.3.1"
	if _, err := c.UpdateImagesStandardReload(context.Background(), "device", "image"); err == nil {
		t.Fatal("accepted unqualified release")
	}
	if _, err := c.UpdateImagesStandardReload(context.Background(), "../device", "image"); err == nil {
		t.Fatal("accepted invalid ID")
	}
	if posts != 1 {
		t.Fatalf("activation posts: %d", posts)
	}
}

type reloadTestAPI struct {
	testModernSWIMAPI
	checks, standardPosts int
}

func (a *reloadTestAPI) CheckStandardReloadProfile(context.Context, string) error {
	a.checks++
	return nil
}
func (a *reloadTestAPI) UpdateImagesStandardReload(ctx context.Context, device, image string) (Task, error) {
	a.standardPosts++
	return a.testSWIMAPI.Activate(ctx, device, image)
}
func TestStandardReloadExecutorUsesPinnedDispatchAndNativeVerification(t *testing.T) {
	e, i, s, a, _, v := executionFixture(t)
	api := &reloadTestAPI{testModernSWIMAPI: testModernSWIMAPI{store: s}}
	e.api = api
	i.APIContract = swimModernContract
	i.ControllerImageVersion = "17.18.03.0.5496"
	i.StandardReloadProfile = "CatalystCenter323"
	for n := 0; n < 11; n++ {
		step(t, e, i)
	}
	if s.record.Phase != swimSucceeded || api.standardPosts != 1 || api.checks < 2 || v.calls != 1 || a.releases != 1 {
		t.Fatalf("unexpected flow phase=%s posts=%d checks=%d verify=%d release=%d", s.record.Phase, api.standardPosts, api.checks, v.calls, a.releases)
	}
}

func TestNormalReloadDowngradeWarningIsExact(t *testing.T) {
	details := []map[string]string{
		{"key": "DESCRIPTION", "value": "Downgrade operations are not supported."},
		{"key": "EXPECTED", "value": "The upgrade image version must be higher than the currently running version 26.02.1."},
		{"key": "ACTUAL", "value": "Selected upgrade image version: 17.18.04"},
		{"key": "STATUS", "value": "WARNING"},
	}
	for _, changed := range []int{-1, 0, 1, 2} {
		copyDetails := make([]map[string]string, len(details))
		for n, d := range details {
			copyDetails[n] = map[string]string{"key": d["key"], "value": d["value"]}
		}
		if changed >= 0 {
			copyDetails[changed]["value"] += " unqualified"
		}
		raw, _ := json.Marshal(map[string]any{"XFSUVersionPathTarget": "17.18.04", "resultDetails": copyDetails})
		var r ReadinessResult
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatal(err)
		}
		if (r.XFSUVersionPathTarget == "17.18.04") != (changed < 0) {
			t.Fatalf("accepted changed detail %d", changed)
		}
	}
	duplicate := append(details, map[string]string{"key": "ACTUAL", "value": "Selected upgrade image version: 17.18.04"})
	raw, _ := json.Marshal(map[string]any{"resultDetails": duplicate})
	var r ReadinessResult
	if json.Unmarshal(raw, &r) == nil {
		t.Fatal("accepted ambiguous target details")
	}
}
