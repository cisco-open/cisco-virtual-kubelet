// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestIntentComplianceTypedReadAndFreshness(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	completed := now.Add(-time.Second)
	base := fmt.Sprintf(`{"deviceUuid":"device","response":[{"deviceUuid":"device","complianceType":"NETWORK_SETTINGS","status":"COMPLIANT","state":"SUCCESS","lastUpdateTime":%d,"sourceInfoList":[{"diffList":[{"configuredValue":"secret-live-value","intendedValue":"secret-intent-value"}]}]},{"deviceUuid":"device","complianceType":"NETWORK_PROFILE","status":"COMPLIANT","state":"SUCCESS","lastUpdateTime":%d}]}`, now.UnixMilli(), now.UnixMilli())
	for _, mode := range []string{"fresh", "stale", "missing-time", "future", "warning", "not-applicable", "failed", "missing-type", "wrong-device", "duplicate", "unknown-status", "fractional", "seconds"} {
		t.Run(mode, func(t *testing.T) {
			raw := base
			switch mode {
			case "stale":
				raw = strings.ReplaceAll(raw, fmt.Sprint(now.UnixMilli()), fmt.Sprint(completed.Add(-time.Second).UnixMilli()))
			case "missing-time":
				raw = strings.ReplaceAll(raw, fmt.Sprint(now.UnixMilli()), "null")
			case "future":
				raw = strings.ReplaceAll(raw, fmt.Sprint(now.UnixMilli()), fmt.Sprint(now.Add(time.Second).UnixMilli()))
			case "warning":
				raw = strings.Replace(raw, `"COMPLIANT"`, `"COMPLIANT_WARNING"`, 1)
			case "not-applicable":
				raw = strings.Replace(raw, `"COMPLIANT"`, `"NOT_APPLICABLE"`, 1)
			case "failed":
				raw = strings.Replace(raw, `"SUCCESS"`, `"FAILED"`, 1)
			case "missing-type":
				raw = `{"deviceUuid":"device","response":[]}`
			case "wrong-device":
				raw = strings.Replace(raw, `"deviceUuid":"device"`, `"deviceUuid":"other"`, 1)
			case "duplicate":
				raw = strings.Replace(raw, `"NETWORK_PROFILE"`, `"NETWORK_SETTINGS"`, 1)
			case "unknown-status":
				raw = strings.Replace(raw, `"COMPLIANT"`, `"new-status"`, 1)
			case "fractional":
				raw = strings.ReplaceAll(raw, fmt.Sprint(now.UnixMilli()), fmt.Sprint(now.UnixMilli())+".5")
			case "seconds":
				raw = strings.ReplaceAll(raw, fmt.Sprint(now.UnixMilli()), fmt.Sprint(now.Unix()))
			}
			evidence, err := parseIntentCompliance([]byte(raw), "device")
			malformed := mode == "wrong-device" || mode == "duplicate" || mode == "unknown-status" || mode == "fractional" || mode == "seconds"
			if malformed {
				if err == nil || evidence != nil {
					t.Fatal("malformed compliance evidence accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			err = freshIntentCompliance(evidence, "device", []string{"NETWORK_SETTINGS", "NETWORK_PROFILE"}, completed, now)
			if mode == "fresh" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil {
				t.Fatal("noncurrent/noncompliant evidence passed")
			}
		})
	}
	reads := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		reads++
		if r.Method != "GET" || r.URL.Path != compliancePath+"device/detail" || r.URL.Query().Get("diffList") != "false" || (r.URL.Query().Get("complianceType") != "" || r.URL.Query().Get("category") != "") {
			t.Error("unbounded or mutating compliance request")
		}
		fmt.Fprint(w, base)
	})
	got, err := c.readIntentCompliance(context.Background(), "device")
	if err != nil || len(got) != 2 || reads != 1 {
		t.Fatal("typed native read failed", err)
	}
}

func TestUnavailableComplianceCannotBecomePositiveEvidence(t *testing.T) {
	for _, raw := range []string{`{"deviceUuid":null,"response":[]}`, `{"deviceUuid":"device","response":[]}`} {
		evidence, err := parseIntentCompliance([]byte(raw), "device")
		if err != nil || len(evidence) != 0 {
			t.Fatal("explicit report absence mishandled", err)
		}
		now := time.Now()
		if err := freshIntentCompliance(evidence, "device", []string{"NETWORK_SETTINGS"}, now.Add(-time.Second), now); err == nil {
			t.Fatal("empty report became convergence evidence")
		}
	}
	for _, raw := range []string{`{"deviceUuid":"other","response":[]}`, `{"deviceUuid":null,"response":[{"deviceUuid":"device","complianceType":"NETWORK_SETTINGS","status":"COMPLIANT","state":"SUCCESS","lastUpdateTime":1792000000000}]}`} {
		if evidence, err := parseIntentCompliance([]byte(raw), "device"); err == nil || evidence != nil {
			t.Fatal("conflicting/unbound identity accepted")
		}
	}
}

func TestUnselectedComplianceDomainsAreNotRetained(t *testing.T) {
	raw := `{"deviceUuid":"device","response":[{"deviceUuid":"device","complianceType":"IMAGE","status":"NON_COMPLIANT","state":"SUCCESS","lastUpdateTime":1792000000000,"sourceInfoList":[{"diffList":[{"configuredValue":"secret"}]}]},{"deviceUuid":"device","complianceType":"NETWORK_SETTINGS","status":"COMPLIANT","state":"SUCCESS","lastUpdateTime":1792000000000}]}`
	evidence, err := parseIntentCompliance([]byte(raw), "device")
	if err != nil || len(evidence) != 1 {
		t.Fatal("networking projection retained unrelated domain", err)
	}
	if _, exists := evidence["IMAGE"]; exists {
		t.Fatal("image evidence leaked into intent verification")
	}
}

func TestIntentComplianceRequiresIndependentCompletionBoundary(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	evidence := map[string]complianceEvidence{
		"NETWORK_SETTINGS": {"device", "NETWORK_SETTINGS", "COMPLIANT", "SUCCESS", now},
	}
	for _, tc := range []struct {
		name      string
		device    string
		kinds     []string
		completed time.Time
		observed  time.Time
	}{
		{"missing-completion", "device", []string{"NETWORK_SETTINGS"}, time.Time{}, now},
		{"missing-observation", "device", []string{"NETWORK_SETTINGS"}, now, time.Time{}},
		{"future-completion", "device", []string{"NETWORK_SETTINGS"}, now.Add(time.Second), now},
		{"empty-selection", "device", nil, now, now},
		{"duplicate-selection", "device", []string{"NETWORK_SETTINGS", "NETWORK_SETTINGS"}, now, now},
		{"unrelated-domain", "device", []string{"IMAGE"}, now, now},
		{"replaced-device", "other", []string{"NETWORK_SETTINGS"}, now, now},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := freshIntentCompliance(evidence, tc.device, tc.kinds, tc.completed, tc.observed); err == nil {
				t.Fatal("unbound evidence or completion boundary accepted")
			}
		})
	}
	// A timestamp equal to the completion boundary is permitted; a report
	// before it must remain pending, even if the status says COMPLIANT.
	if err := freshIntentCompliance(evidence, "device", []string{"NETWORK_SETTINGS"}, now, now); err != nil {
		t.Fatal("exact completion boundary rejected", err)
	}
	if err := freshIntentCompliance(evidence, "device", []string{"NETWORK_SETTINGS"}, now.Add(time.Millisecond), now.Add(time.Second)); err == nil {
		t.Fatal("compliant report predating completion accepted")
	}
}
