// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestReadinessWireStatusVariants(t *testing.T) {
	for _, tc := range []struct{ body, status string }{
		{`{"id":"check-1","status":"SUCCESS"}`, "SUCCESS"},
		{`{"id":"check-1","resultDetails":[{"key":"DESCRIPTION","value":"sensitive CLI output"},{"key":"STATUS","value":"success"}]}`, "SUCCESS"},
		{`{"resultDetails":[{"key":"STATUS","value":"warning"}]}`, "WARNING"},
		{`{"resultDetails":{"key":"STATUS","value":"FAILED"}}`, "FAILED"},
		{`{"status":"SUCCESS","resultDetails":[{"key":"STATUS","value":"success"}]}`, "SUCCESS"},
		{`{"resultDetails":null}`, ""},
		{`{}`, ""},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var result ReadinessResult
			if err := json.Unmarshal([]byte(tc.body), &result); err != nil {
				t.Fatal(err)
			}
			if result.Status != tc.status {
				t.Fatalf("status %q, want %q", result.Status, tc.status)
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "sensitive") || strings.Contains(string(encoded), "resultDetails") {
				t.Fatal("retained remote CLI output")
			}
		})
	}
	for _, body := range []string{
		`{"status":"SUCCESS","resultDetails":[{"key":"STATUS","value":"warning"}]}`,
		`{"resultDetails":[{"key":"STATUS","value":"SUCCESS"},{"key":"STATUS","value":"SUCCESS"}]}`,
		`{"resultDetails":[{"key":"STATUS","value":""}]}`,
		`{"resultDetails":"invalid"}`,
	} {
		var result ReadinessResult
		if json.Unmarshal([]byte(body), &result) == nil {
			t.Fatalf("accepted ambiguous status: %s", body)
		}
	}
}

func TestReadinessDoesNotTreatEmptySuccessfulTaskAsReady(t *testing.T) {
	// Observed on the 3.2.3 lab appliance: the task completes without error,
	// but performs no checks when no golden image is assigned.
	state, err := parseSWIMTask([]byte(`{"response":{"id":"task-1","isError":false,"endTime":1791450515464,"progress":"Image is not tagged as golden for readiness Check"}}`), "task-1")
	if err != nil || !state.complete {
		t.Fatalf("task parsing: %+v %v", state, err)
	}
	now := time.Now()
	if validateReadinessResults(nil, "device-1", "task-1", now.Add(-time.Minute), now, time.Hour) == nil {
		t.Fatal("successful task with no validation evidence authorized readiness")
	}
}

func TestReadinessEvidenceIsBoundAndFresh(t *testing.T) {
	now := time.Now().Truncate(time.Millisecond)
	start := now.Add(-time.Minute)
	good := ReadinessResult{ID: "check-1", ParentID: "task-1", DeviceID: "device-1", Operation: "READINESS_CHECK", Type: "PRE_VALIDATION", Name: "Disk space", Status: "SUCCESS", StartTime: start.UnixMilli(), EndTime: now.UnixMilli()}
	if err := validateReadinessResults([]ReadinessResult{good}, "device-1", "task-1", start, now, time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*ReadinessResult)
	}{
		{"other device", func(r *ReadinessResult) { r.DeviceID = "device-2" }},
		{"other run", func(r *ReadinessResult) { r.ParentID = "task-old" }},
		{"other operation", func(r *ReadinessResult) { r.Operation = "ACTIVATION" }},
		{"other type", func(r *ReadinessResult) { r.Type = "POST_VALIDATION" }},
		{"missing identity", func(r *ReadinessResult) { r.ID = "" }},
		{"missing name", func(r *ReadinessResult) { r.Name = "" }},
		{"old result", func(r *ReadinessResult) { r.StartTime = start.Add(-time.Minute).UnixMilli() }},
		{"future result", func(r *ReadinessResult) { r.EndTime = now.Add(time.Minute).UnixMilli() }},
		{"unfinished", func(r *ReadinessResult) { r.EndTime = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := good
			tc.change(&r)
			if validateReadinessResults([]ReadinessResult{r}, "device-1", "task-1", start, now, time.Hour) == nil {
				t.Fatal("accepted invalid evidence")
			}
		})
	}
	for _, status := range []string{"", "WARNING", "SKIPPED", "FAILED", "PARTIAL_SUCCESS", "STARTED", "PENDING", "ABORTED", "new-status"} {
		t.Run(status, func(t *testing.T) {
			r := good
			r.Status = status
			if validateReadinessResults([]ReadinessResult{r}, "device-1", "task-1", start, now, time.Hour) == nil {
				t.Fatal("accepted non-success")
			}
		})
	}
	if validateReadinessResults([]ReadinessResult{good, good}, "device-1", "task-1", start, now, time.Hour) == nil {
		t.Fatal("accepted duplicate validation")
	}
	if validateReadinessResults([]ReadinessResult{good}, "device-1", "task-1", start, now, 30*time.Second) == nil {
		t.Fatal("accepted expired run")
	}
}

func TestReadinessAPIContracts(t *testing.T) {
	posts, gets := 0, 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case authPath:
			fmt.Fprint(w, `{"Token":"test-token"}`)
		case networkDeviceImagesPath + "device-1/readinessChecks":
			posts++
			var body map[string]any
			if r.Method != http.MethodPost || json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 0 {
				t.Error("invalid readiness request")
			}
			fmt.Fprint(w, `{"response":{"taskId":"task-1","url":"https://untrusted.example/task-1"}}`)
		case networkDeviceImagesPath + "validationResults":
			gets++
			q := r.URL.Query()
			if r.Method != http.MethodGet || q.Get("networkDeviceId") != "device-1" || q.Get("operationType") != "READINESS_CHECK" || q.Get("offset") != "1" || q.Get("limit") != "500" {
				t.Errorf("invalid query: %v", q)
			}
			fmt.Fprint(w, `{"response":[{"id":"check-1","networkDeviceId":"device-1","operationType":"READINESS_CHECK"}]}`)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			http.NotFound(w, r)
		}
	})
	task, err := c.StartReadinessCheck(context.Background(), "device-1")
	if err != nil || task.ID != "task-1" {
		t.Fatalf("task %+v: %v", task, err)
	}
	results, err := c.ListReadinessResults(context.Background(), "device-1")
	if err != nil || len(results) != 1 || posts != 1 || gets != 1 {
		t.Fatalf("results %v: %v", results, err)
	}
	if _, err := c.StartReadinessCheck(context.Background(), "../device-1"); err == nil {
		t.Fatal("accepted traversal")
	}
	if _, err := c.ListReadinessResults(context.Background(), "device-1?other=x"); err == nil {
		t.Fatal("accepted query injection")
	}
}

func TestReadinessPOSTIsNotRetried(t *testing.T) {
	for _, code := range []int{401, 403, 404, 500, 503} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			posts := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"token"}`)
					return
				}
				posts++
				w.WriteHeader(code)
			})
			if _, err := c.StartReadinessCheck(context.Background(), "device-1"); err == nil {
				t.Fatal("expected error")
			}
			if posts != 1 {
				t.Fatalf("POST retried %d times", posts)
			}
		})
	}
}

func TestReadinessResultsRejectMalformedAndMismatchedResponses(t *testing.T) {
	for _, body := range []string{`{}`, `{"response":null}`, `{"response":{}}`, `{"response":[{"id":"check-1","networkDeviceId":"other","operationType":"READINESS_CHECK"}]}`, `{"response":[{"id":"check-1","networkDeviceId":"device-1","operationType":"ACTIVATION"}]}`, `{"response":[{"networkDeviceId":"device-1","operationType":"READINESS_CHECK"}]}`} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"token"}`)
					return
				}
				fmt.Fprint(w, body)
			})
			if _, err := c.ListReadinessResults(context.Background(), "device-1"); err == nil {
				t.Fatal("accepted invalid results")
			}
		})
	}
}

func TestReadinessPaginationPreservesBindingAndRejectsRepeatedPages(t *testing.T) {
	for _, repeat := range []bool{false, true} {
		t.Run(fmt.Sprint(repeat), func(t *testing.T) {
			gets := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"token"}`)
					return
				}
				gets++
				q := r.URL.Query()
				if q.Get("networkDeviceId") != "device-1" || q.Get("operationType") != "READINESS_CHECK" || q.Get("limit") != "500" || q.Get("offset") != fmt.Sprint(1+(gets-1)*500) {
					t.Errorf("lost pagination filter: %v", q)
				}
				items := make([]ReadinessResult, 0, 500)
				if gets == 1 || repeat {
					for i := 0; i < 500; i++ {
						items = append(items, ReadinessResult{ID: fmt.Sprintf("check-%d", i), DeviceID: "device-1", Operation: "READINESS_CHECK"})
					}
				}
				if err := json.NewEncoder(w).Encode(map[string]any{"response": items}); err != nil {
					t.Error(err)
				}
			})
			results, err := c.ListReadinessResults(context.Background(), "device-1")
			if gets != 2 {
				t.Fatalf("unexpected page requests: %d", gets)
			}
			if repeat {
				if err == nil || results != nil {
					t.Fatal("repeated page published partial evidence")
				}
			} else if err != nil || len(results) != 500 {
				t.Fatalf("pagination: %d results, %v", len(results), err)
			}
		})
	}
}

func TestDeviceImageDetailsRejectInvalidBinding(t *testing.T) {
	for _, body := range []string{`{}`, `{"response":null}`, `{"response":{"id":"other","networkDevice":{"id":"286315874"}}}`, `{"response":{"id":"device-1"}}`} {
		t.Run(body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"token"}`)
					return
				}
				fmt.Fprint(w, body)
			})
			if _, err := c.GetDeviceImageDetails(context.Background(), "device-1"); err == nil {
				t.Fatal("accepted invalid product binding")
			}
		})
	}
}

func TestImageProductRequiresBoundProductIdentifier(t *testing.T) {
	device := Device{ID: "device-1", ManagementIP: "192.0.2.10", PlatformID: "C9300-24P"}
	details := DeviceImageDetails{ID: device.ID, ManagementAddress: device.ManagementIP}
	details.NetworkDevice.ID = "286315874"
	image := Image{ID: "image-1", Family: "CAT9K", ApplicableDevices: []ImageProduct{{ID: "286315874", ProductIDs: []string{"C9300-24T"}}}}
	// The live catalogue omits the 24P PID; an exact controller-bound product
	// identifier is still a valid product-family association.
	if err := validateImageProduct(image, device, details); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		change func(*DeviceImageDetails)
	}{
		{"wrong device", func(d *DeviceImageDetails) { d.ID = "other" }},
		{"wrong address", func(d *DeviceImageDetails) { d.ManagementAddress = "192.0.2.11" }},
		{"wrong product", func(d *DeviceImageDetails) { d.NetworkDevice.ID = "286319595" }},
		{"wrong supervisor", func(d *DeviceImageDetails) { d.NetworkDevice.ID = "286315874-286319595" }},
		{"missing product", func(d *DeviceImageDetails) { d.NetworkDevice.ID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			copy := details
			tc.change(&copy)
			if validateImageProduct(image, device, copy) == nil {
				t.Fatal("accepted mismatched binding")
			}
		})
	}
	image.ApplicableDevices = nil
	if validateImageProduct(image, device, details) == nil {
		t.Fatal("family name is not product proof")
	}
}
