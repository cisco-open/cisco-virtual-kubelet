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

const executionSiteModel = `catalyst_center:
  sites:
    areas: [{name: New, parent_name: Global}]
    buildings: [{name: Building, parent_name: Global/New, address: Example, country: Germany, latitude: 52.5, longitude: 13.4}]
    floors: [{name: Floor, parent_name: Global/New/Building, floor_number: 1, rf_model: Free Space, width: 10, length: 15, height: 3, units_of_measure: meters}]
`

func TestSiteExecutionOrdersDependenciesAndUsesModernEndpoints(t *testing.T) {
	desired, err := parseSites([]byte(executionSiteModel))
	if err != nil {
		t.Fatal(err)
	}
	actual := sampleSites()[:1]
	for i, endpoint := range []string{"/dna/intent/api/v1/areas", "/dna/intent/api/v2/buildings", "/dna/intent/api/v2/floors"} {
		m, err := nextSiteMutation(desired, actual)
		if err != nil || m == nil {
			t.Fatalf("step %d: %v", i, err)
		}
		if m.endpoint != endpoint || m.method != "POST" || m.action != "Create" {
			t.Fatalf("wrong operation: %+v", m)
		}
		var payload map[string]any
		if err := json.Unmarshal(m.body, &payload); err != nil {
			t.Fatal(err)
		}
		if payload["parentId"] != actual[len(actual)-1].ID {
			t.Fatal("unresolved parent identity")
		}
		if i == 2 && (payload["floorNumber"] != float64(1) || payload["unitsOfMeasure"] != "meters" || payload["rfModel"] != "Free Space") {
			t.Fatalf("wrong floor payload: %s", m.body)
		}
		if i == 1 && (payload["latitude"] != 52.5 || payload["country"] != "Germany") {
			t.Fatalf("wrong building payload: %s", m.body)
		}
		// Echo the API projection to represent an authoritative read next step.
		var site Site
		if err := json.Unmarshal(m.body, &site); err != nil {
			t.Fatal(err)
		}
		site.ID, site.NameHierarchy, site.Type = fmt.Sprintf("site-%d", i), m.desired.path, m.desired.kind
		actual = append(actual, site)
	}
	if m, err := nextSiteMutation(desired, actual); err != nil || m != nil {
		t.Fatalf("verified hierarchy planned again: %+v %v", m, err)
	}
}

func TestSiteExecutionValidatesWholeIntentBeforeFirstParent(t *testing.T) {
	desired, err := parseSites([]byte(strings.ReplaceAll(executionSiteModel, "floor_number: 1, ", "")))
	if err != nil {
		t.Fatal(err)
	}
	if m, err := nextSiteMutation(desired, sampleSites()[:1]); err == nil || m != nil {
		t.Fatal("parent planned before invalid descendant discovered")
	}
}

func TestSiteExecutionUpdatePreservesUnselectedBuildingAttributes(t *testing.T) {
	desired, err := parseSites([]byte(`catalyst_center: {sites: {buildings: [{name: Building, parent_name: Global/Lab, address: New}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	actual := sampleSites()
	address, country, lat, lon := "Old", "Germany", 52.5, 13.4
	actual[2].Address, actual[2].Country, actual[2].Latitude, actual[2].Longitude = &address, &country, &lat, &lon
	m, err := nextSiteMutation(desired, actual)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(m.body, &payload); err != nil {
		t.Fatal(err)
	}
	if m.method != "PUT" || m.id != "building" || m.endpoint != "/dna/intent/api/v2/buildings/building" || payload["address"] != "New" || payload["latitude"] != lat || payload["longitude"] != lon || payload["country"] != country {
		t.Fatalf("unsafe update: %+v %s", m, m.body)
	}
}

func TestSiteSubmissionNeverReplaysMutatingRequests(t *testing.T) {
	for _, outcome := range []string{"accepted", "unauthorized", "unavailable", "redirect", "invalid", "no-receipt"} {
		t.Run(outcome, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "POST" || r.URL.Path != "/dna/intent/api/v1/areas" {
					t.Errorf("unexpected mutation %s %s", r.Method, r.URL.Path)
				}
				switch outcome {
				case "unauthorized":
					w.WriteHeader(401)
				case "unavailable":
					w.WriteHeader(503)
				case "redirect":
					w.Header().Set("Location", "/replayed")
					w.WriteHeader(307)
				case "invalid":
					fmt.Fprint(w, `{"response":{"taskId":"../foreign"}}`)
				case "no-receipt":
					fmt.Fprint(w, `{}`)
				default:
					fmt.Fprint(w, `{"response":{"taskId":"task-1","url":"https://foreign.example/task"}}`)
				}
			})
			desired, _ := parseSites([]byte(executionSiteModel))
			m, err := nextSiteMutation(desired, sampleSites()[:1])
			if err != nil {
				t.Fatal(err)
			}
			task, err := c.submitSiteMutation(context.Background(), m)
			if (err == nil) != (outcome == "accepted") || calls != 1 || task.URL != "" {
				t.Fatalf("task=%+v err=%v calls=%d", task, err, calls)
			}
		})
	}
}

func TestConfigurationTaskRequiresCorrelatedTerminalReceipt(t *testing.T) {
	start := time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	for _, outcome := range []string{"complete", "clock-ahead", "clock-behind", "running", "wrong-id", "old", "future", "missing-error", "failed", "end-before-start", "partial"} {
		t.Run(outcome, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != taskPath+"task-1" || r.Method != "GET" {
					t.Errorf("unexpected task read")
				}
				task := map[string]any{"id": "task-1", "startTime": start.UnixMilli(), "endTime": start.Add(time.Second).UnixMilli(), "isError": false}
				switch outcome {
				case "clock-ahead":
					task["startTime"], task["endTime"] = start.Add(76*time.Second).UnixMilli(), start.Add(77*time.Second).UnixMilli()
				case "clock-behind":
					task["startTime"], task["endTime"] = start.Add(-76*time.Second).UnixMilli(), start.Add(-75*time.Second).UnixMilli()
				case "running":
					delete(task, "endTime")
				case "wrong-id":
					task["id"] = "other"
				case "old":
					task["startTime"] = start.Add(-3 * time.Minute).UnixMilli()
				case "future":
					task["endTime"] = time.Now().Add(time.Hour).UnixMilli()
				case "missing-error":
					delete(task, "isError")
				case "failed":
					task["isError"] = true
				case "end-before-start":
					task["endTime"] = start.Add(-time.Second).UnixMilli()
				case "partial":
					w.WriteHeader(206)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": task})
			})
			complete, err := c.configurationTaskComplete(context.Background(), "task-1", start, time.Now())
			success := outcome == "complete" || outcome == "clock-ahead" || outcome == "clock-behind"
			if complete != success || (err == nil) != (success || outcome == "running") {
				t.Fatalf("complete=%v err=%v", complete, err)
			}
		})
	}
}

func TestSiteVerificationChecksIdentityAndAttributes(t *testing.T) {
	for _, outcome := range []string{"match", "drift", "replaced", "missing", "partial"} {
		t.Run(outcome, func(t *testing.T) {
			desired, _ := parseSites([]byte(`catalyst_center: {sites: {buildings: [{name: Building, parent_name: Global/Lab, address: New}]}}`))
			actual := sampleSites()
			address := "Old"
			actual[2].Address = &address
			m, err := nextSiteMutation(desired, actual)
			if err != nil {
				t.Fatal(err)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				address = "New"
				switch outcome {
				case "drift":
					address = "Old"
				case "replaced":
					actual[2].ID = "replacement"
				case "missing":
					actual = actual[:2]
				case "partial":
					w.WriteHeader(206)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": actual})
			})
			verified, err := c.verifySiteMutation(context.Background(), m)
			if verified != (outcome == "match") {
				t.Fatalf("verified=%v err=%v", verified, err)
			}
			if (outcome == "replaced" || outcome == "partial") && err == nil {
				t.Fatal("invalid observation accepted")
			}
		})
	}
}

func TestSiteExecutionRejectsOversizedNameBeforeSubmission(t *testing.T) {
	desired, err := parseSites([]byte(`catalyst_center: {sites: {areas: [{name: ` + strings.Repeat("a", 41) + `, parent_name: Global}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if m, err := nextSiteMutation(desired, sampleSites()[:1]); err == nil || m != nil {
		t.Fatal("oversized site name passed execution preflight")
	}
}
