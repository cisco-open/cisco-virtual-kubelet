// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cisco/virtual-kubelet-cisco/internal/softwarelifecycle"
)

func TestRejectAmbiguousInput(t *testing.T) {
	for _, input := range []string{`{"limits":{"maxFiles":1,"maxFiles":2}}`, `{"unknown":true}`, `{} {}`, `{"observation":`, `[]`, strings.Repeat(" ", (8<<20)+1)} {
		var out, diagnostic bytes.Buffer
		if code := run(strings.NewReader(input), &out, &diagnostic, time.Now()); code != 1 || out.Len() != 0 {
			t.Fatalf("invalid input accepted: code=%d", code)
		}
	}
}

func TestCompleteEvidenceProducesReviewableReport(t *testing.T) {
	now := time.Now().UTC()
	in := softwarelifecycle.PreparationPlanInput{
		Observation: softwarelifecycle.PreparationObservation{
			DeviceUID: "device", PhysicalIdentity: "serial", ControllerUID: "controller", TargetImageID: "target",
			PolicyUID: "policy", PolicyGeneration: 1, ObservedAt: now, Revision: "1", Volume: "flash:", FreeBytes: 100, RequiredFreeBytes: 200,
			Quiescent: true, Complete: map[string]bool{"filesystem": true, "install": true, "boot": true, "target": true, "rollback": true, "preparations": true, "applications": true},
			Files: []softwarelifecycle.PreparationFile{
				{Path: "flash:rollback.bin", Size: 100, SHA256: strings.Repeat("a", 64), Kind: "ImageArchive", RetainedRollbackImage: "rollback"},
				{Path: "flash:old.bin", Size: 100, SHA256: strings.Repeat("b", 64), Kind: "ImageArchive"},
			},
		},
		Limits: softwarelifecycle.PreparationLimits{MaxFiles: 1, MaxBytes: 100, PreserveRollbackImages: 1, MaxAgeSeconds: 60},
	}
	input, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out, diagnostic bytes.Buffer
	if code := run(bytes.NewReader(input), &out, &diagnostic, now); code != 0 {
		t.Fatalf("code=%d: %s %s", code, &out, &diagnostic)
	}
	var report softwarelifecycle.PreparationPlan
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if report.Mode != "PlanOnly" || len(report.Candidates) != 1 || report.Candidates[0].Path != "flash:old.bin" || report.InputSHA256 == "" {
		t.Fatalf("bad report: %+v", report)
	}
}

func TestIncompleteEvidenceReportsBlockers(t *testing.T) {
	var out, diagnostic bytes.Buffer
	if code := run(strings.NewReader(`{}`), &out, &diagnostic, time.Now()); code != 2 {
		t.Fatalf("code=%d diagnostics=%s", code, diagnostic.String())
	}
	if !strings.Contains(out.String(), `"mode": "PlanOnly"`) || !strings.Contains(out.String(), `"candidates": []`) {
		t.Fatal(out.String())
	}
}
