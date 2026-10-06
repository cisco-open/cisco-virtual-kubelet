// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package softwarelifecycle

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	configtransport "github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	lifecycle "github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

func TestPreparationRetirementRequiresCompleteQuiescentInventory(t *testing.T) {
	const base = `{"install-location-information":[{"oper-state":{"sys-activity":"install-no-activity"},"install-version-info":[{"version":"17.18.03","current":"install-version-state-provisioned-committed"},{"version":"17.18.02","current":"install-version-state-installed"}]}]}`
	for _, tc := range []struct {
		name, raw string
		valid     bool
		state     lifecycle.InventoryState
	}{
		{"inactive", base, true, lifecycle.InventoryStateInstalled},
		{"absent", strings.Replace(base, `,{"version":"17.18.02","current":"install-version-state-installed"}`, "", 1), true, lifecycle.InventoryStateAbsent},
		{"active-installer", strings.Replace(base, "install-no-activity", "install-add", 1), false, ""},
		{"unknown-installer", strings.Replace(base, "install-no-activity", "", 1), false, ""},
		{"target-in-progress", strings.Replace(base, "install-version-state-installed", "install-version-state-in-progress", 1), false, ""},
		{"running-uncommitted", strings.Replace(base, "provisioned-committed", "provisioned-uncommitted", 1), false, ""},
		{"target-active", strings.Replace(base, "install-version-state-installed", "install-version-state-provisioned-committed", 1), false, ""},
		{"missing-locations", `{}`, false, ""},
		{"empty-locations", `{"install-location-information":[]}`, false, ""},
		{"malformed", `{"install-location-information":{}}`, false, ""},
		{"unidentified-target", strings.Replace(base, `"version":"17.18.02"`, `"version":""`, 1), false, ""},
		{"duplicate-identity", strings.Replace(base, "17.18.02", "17.18.03", 1), false, ""},
		{"unknown-unrelated-image", strings.Replace(base, `"version":"17.18.02","current":"install-version-state-installed"`, `"version":"17.18.01","current":"unknown"`, 1), false, ""},
		{"unknown-operation", strings.TrimSuffix(base, "}") + `,"install-oper":[{"op-status":"unknown"}]}`, false, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, err := New(&fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installOperDataPath: []byte(tc.raw)}})
			if err != nil {
				t.Fatal(err)
			}
			got, err := a.ObservePreparationRetirement(context.Background(), lifecycle.PreparationRetirementRequest{TargetVersion: "17.18.02", RunningVersion: "17.18.03"})
			if (err == nil) != tc.valid {
				t.Fatalf("result=%+v error=%v", got, err)
			}
			if tc.valid && (got.TargetState != tc.state || !strings.HasPrefix(got.EvidenceHash, "sha256:") || len(got.EvidenceHash) != 71) {
				t.Fatalf("incomplete proof: %+v", got)
			}
		})
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(base), &root); err != nil {
		t.Fatal(err)
	}
	locations := root["install-location-information"].([]any)
	// One apparently healthy member must not hide a member without a committed OS.
	root["install-location-information"] = append(locations, map[string]any{"oper-state": map[string]any{"sys-activity": "install-no-activity"}, "install-version-info": []any{}})
	raw, _ := json.Marshal(root)
	if _, err := preparationRetirementFromJSON(raw, lifecycle.PreparationRetirementRequest{TargetVersion: "17.18.02", RunningVersion: "17.18.03"}, time.Time{}, time.Time{}); err == nil {
		t.Fatal("incomplete member accepted")
	}
}

func TestPreparationRetirementCorroboratesOnlyTheRetainedAttempt(t *testing.T) {
	started := time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)
	request := lifecycle.PreparationRetirementRequest{
		TargetVersion: "17.18.02.0.4112.1766116039", RunningVersion: "17.18.03",
		SourceSize: 1247897709, InstallStartedAt: started, PreparedAt: started.Add(3 * time.Minute),
	}
	raw := interruptedInstallResponse("install-no-activity", "install-state-added", "install-package-verify-ok",
		"1247897709", "install-op-succ", "op-complete", started)
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	root := envelope["Cisco-IOS-XE-install-oper:install-oper-data"].(map[string]any)
	location := root["install-location-information"].([]any)[0].(map[string]any)
	location["install-version-info"] = append(location["install-version-info"].([]any), map[string]any{
		"version": request.RunningVersion, "current": "install-version-state-provisioned-committed",
	})
	base := string(mustJSON(envelope))
	for _, tc := range []struct {
		name    string
		raw     string
		change  func(*lifecycle.PreparationRetirementRequest)
		noClock bool
		valid   bool
	}{
		{name: "exact completed add after a two-day hold", valid: true},
		{name: "missing response clock", noClock: true},
		{name: "wrong source size", change: func(r *lifecycle.PreparationRetirementRequest) { r.SourceSize++ }},
		{name: "missing source size", change: func(r *lifecycle.PreparationRetirementRequest) { r.SourceSize = 0 }},
		{name: "short version is not exact", change: func(r *lifecycle.PreparationRetirementRequest) { r.TargetVersion = "17.18.02" }},
		{name: "missing interval", change: func(r *lifecycle.PreparationRetirementRequest) { r.InstallStartedAt = time.Time{} }},
		{name: "reversed interval", change: func(r *lifecycle.PreparationRetirementRequest) { r.PreparedAt = r.InstallStartedAt.Add(-time.Minute) }},
		{name: "future receipt", change: func(r *lifecycle.PreparationRetirementRequest) { r.PreparedAt = started.Add(72 * time.Hour) }},
		{name: "unverified source", raw: strings.ReplaceAll(base, "install-package-verify-ok", "install-package-verify-fail")},
		{name: "package not added", raw: strings.ReplaceAll(base, "install-state-added", "install-state-activated")},
		{name: "installer busy", raw: strings.ReplaceAll(base, "install-no-activity", "install-add")},
		{name: "running uncommitted", raw: strings.ReplaceAll(base, "provisioned-committed", "provisioned-uncommitted")},
		{name: "failed add", raw: strings.ReplaceAll(base, "install-op-succ", "install-op-fail")},
		{name: "later reinstall does not inherit receipt", raw: strings.ReplaceAll(base, "2026-10-02", "2026-10-03")},
		{name: "old history outside preparation", raw: strings.ReplaceAll(base, "2026-10-02", "2026-10-01")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := request
			if tc.change != nil {
				tc.change(&r)
			}
			body := tc.raw
			if body == "" {
				body = base
			}
			now := started.Add(48 * time.Hour)
			deviceTime := now
			if tc.noClock {
				deviceTime = time.Time{}
			}
			a, err := New(&timedTransport{
				fakeTransport:    &fakeTransport{kind: configtransport.KindRESTCONF, responses: map[string][]byte{installOperDataPath: []byte(body)}},
				deviceObservedAt: deviceTime, localObservedAt: now,
			})
			if err != nil {
				t.Fatal(err)
			}
			got, err := a.ObservePreparationRetirement(context.Background(), r)
			if (err == nil) != tc.valid {
				t.Fatalf("result=%+v error=%v", got, err)
			}
			if tc.valid && (got.TargetState != lifecycle.InventoryStateInstalled || len(got.EvidenceHash) != 71) {
				t.Fatalf("missing exact proof: %+v", got)
			}
		})
	}
	for _, name := range []string{"missing history", "duplicate add", "later unrelated operation", "unknown operation", "unsettled unrelated image", "incomplete member"} {
		t.Run(name, func(t *testing.T) {
			var envelope map[string]any
			if err := json.Unmarshal([]byte(base), &envelope); err != nil {
				t.Fatal(err)
			}
			root := envelope["Cisco-IOS-XE-install-oper:install-oper-data"].(map[string]any)
			history := root["install-oper-hist"].([]any)
			location := root["install-location-information"].([]any)[0].(map[string]any)
			switch name {
			case "missing history":
				root["install-oper-hist"] = []any{}
			case "duplicate add":
				root["install-oper-hist"] = append(history, history[0])
			case "later unrelated operation":
				root["install-oper-hist"] = append(history, map[string]any{
					"op-uuid": "other", "op-status": "install-op-succ", "op-done": "op-complete",
					"start-time": started.Add(time.Hour).Format(time.RFC3339),
					"end-time":   started.Add(2 * time.Hour).Format(time.RFC3339),
				})
			case "unknown operation":
				root["install-oper"] = []any{map[string]any{"op-status": "unknown"}}
			case "unsettled unrelated image":
				location["install-version-info"] = append(location["install-version-info"].([]any), map[string]any{
					"version": "17.18.01", "current": "install-version-state-in-progress",
				})
			case "incomplete member":
				root["install-location-information"] = append(root["install-location-information"].([]any), map[string]any{})
			}
			if _, err := preparationRetirementFromJSON(mustJSON(envelope), request, started.Add(48*time.Hour), started.Add(48*time.Hour)); err == nil {
				t.Fatal("ambiguous evidence released ownership")
			}
		})
	}
	// Response timestamps map both ends of the original immutable receipt to
	// device time, not to the much later recovery request time.
	shifted := strings.ReplaceAll(base, "T01:", "T07:")
	if _, err := preparationRetirementFromJSON([]byte(shifted), request, started.Add(54*time.Hour), started.Add(48*time.Hour)); err != nil {
		t.Fatalf("native clock mapping rejected exact add: %v", err)
	}
	upgrade := strings.ReplaceAll(base, "17.18.03", "17.18.01")
	request.RunningVersion = "17.18.01"
	if _, err := preparationRetirementFromJSON([]byte(upgrade), request, started.Add(48*time.Hour), started.Add(48*time.Hour)); err != nil {
		t.Fatalf("upgrade-direction inactive inventory rejected: %v", err)
	}
}

func TestPreparationRetirementIOSXECapturedInventoryShape(t *testing.T) {
	// Minimized projection of the 2 October .101 preparation capture: all
	// locations, versions, package names/states/sizes and operation summaries
	// are retained; credentials, image build metadata and transaction detail
	// are excluded. The clock/receipt envelope below is a controlled fixture,
	// not a claim of new physical recovery or original HTTP-Date provenance.
	raw, err := os.ReadFile("testdata/retirement-iosxe-171803.json")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 2, 2, 5, 0, 0, time.UTC)
	request := lifecycle.PreparationRetirementRequest{
		TargetVersion: "17.18.03.0.5496.1776157760", RunningVersion: "17.18.02.0.4112.1766116039",
		SourceSize: 1249368115, InstallStartedAt: start, PreparedAt: start.Add(3 * time.Minute),
	}
	got, err := preparationRetirementFromJSON(raw, request, start.Add(time.Hour), start.Add(time.Hour))
	if err != nil || got.TargetState != lifecycle.InventoryStateInstalled {
		t.Fatalf("captured inactive add rejected: %+v %v", got, err)
	}
	if _, err := preparationRetirementFromJSON(raw, request, time.Time{}, time.Time{}); err == nil {
		t.Fatal("capture without response clock was treated as live recovery proof")
	}
}
