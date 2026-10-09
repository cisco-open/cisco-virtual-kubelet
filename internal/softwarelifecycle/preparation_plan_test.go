// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package softwarelifecycle

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func preparationFixture() (PreparationPlanInput, time.Time) {
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	in := PreparationPlanInput{
		Observation: PreparationObservation{
			DeviceUID: "device-uid", PhysicalIdentity: "serial", ControllerUID: "controller-uid", TargetImageID: "image-id",
			PolicyUID: "policy-uid", PolicyGeneration: 1, Revision: "inventory-1", ObservedAt: now.Add(-time.Second),
			Volume: "flash:", FreeBytes: 100, RequiredFreeBytes: 150, Quiescent: true,
			Complete: map[string]bool{}, Files: []PreparationFile{
				{Path: "flash:rollback.bin", Size: 100, SHA256: strings.Repeat("a", 64), Kind: "ImageArchive", RetainedRollbackImage: "17.18.02"},
				{Path: "flash:old.bin", Size: 100, SHA256: strings.Repeat("b", 64), Kind: "ImageArchive"},
				{Path: "flash:packages.conf", Size: 10, Kind: "Configuration", References: []string{"boot", "committed"}},
			},
		},
		Limits: PreparationLimits{MaxFiles: 1, MaxBytes: 100, HeadroomBytes: 10, PreserveRollbackImages: 1, MaxAgeSeconds: 60},
	}
	for _, d := range preparationDomains {
		in.Observation.Complete[d] = true
	}
	return in, now
}

func TestPreparationPlanProtectsAndBinds(t *testing.T) {
	in, now := preparationFixture()
	before, _ := json.Marshal(in)
	p := PlanPreparation(in, now)
	if len(p.Blockers) != 0 || !p.SpaceSatisfied || len(p.Candidates) != 1 || p.Candidates[0].Path != "flash:old.bin" || p.Mode != "PlanOnly" || p.RequiredFreeBytes != 160 {
		t.Fatalf("unexpected plan: %+v", p)
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("mutated caller evidence")
	}
	if err := ValidatePreparationPlan(p.InputSHA256, in, now); err != nil {
		t.Fatal(err)
	}
	in.Observation.Files[0], in.Observation.Files[2] = in.Observation.Files[2], in.Observation.Files[0]
	if q := PlanPreparation(in, now); q.InputSHA256 != p.InputSHA256 {
		t.Fatal("ordering changed evidence digest")
	}
}

func TestPreparationPlanFailsClosed(t *testing.T) {
	cases := map[string]func(*PreparationPlanInput){
		"partial filesystem": func(i *PreparationPlanInput) { i.Observation.Complete["filesystem"] = false },
		"partial receipts":   func(i *PreparationPlanInput) { delete(i.Observation.Complete, "preparations") },
		"unresolved receipt": func(i *PreparationPlanInput) { i.Observation.Unresolved = []string{"consumption unknown"} },
		"stale":              func(i *PreparationPlanInput) { i.Observation.ObservedAt = i.Observation.ObservedAt.Add(-time.Hour) },
		"future":             func(i *PreparationPlanInput) { i.Observation.ObservedAt = i.Observation.ObservedAt.Add(time.Hour) },
		"not quiescent":      func(i *PreparationPlanInput) { i.Observation.Quiescent = false },
		"identity":           func(i *PreparationPlanInput) { i.Observation.DeviceUID = "" },
		"duplicate": func(i *PreparationPlanInput) {
			i.Observation.Files = append(i.Observation.Files, i.Observation.Files[1])
		},
		"path traversal":      func(i *PreparationPlanInput) { i.Observation.Files[1].Path = "flash:../old.bin" },
		"alias":               func(i *PreparationPlanInput) { i.Observation.Files[1].Path = "flash:/old.bin" },
		"other volume":        func(i *PreparationPlanInput) { i.Observation.Files[1].Path = "bootflash:old.bin" },
		"digest":              func(i *PreparationPlanInput) { i.Observation.Files[1].SHA256 = "" },
		"rollback":            func(i *PreparationPlanInput) { i.Observation.Files[0].RetainedRollbackImage = "" },
		"overflow":            func(i *PreparationPlanInput) { i.Observation.RequiredFreeBytes = ^uint64(0) },
		"no requirement":      func(i *PreparationPlanInput) { i.Observation.RequiredFreeBytes = 0 },
		"unbounded files":     func(i *PreparationPlanInput) { i.Limits.MaxFiles = 0 },
		"unbounded freshness": func(i *PreparationPlanInput) { i.Limits.MaxAgeSeconds = 1 << 62 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			in, now := preparationFixture()
			change(&in)
			p := PlanPreparation(in, now)
			if len(p.Blockers) == 0 || len(p.Candidates) != 0 || p.SpaceSatisfied {
				t.Fatalf("did not fail closed: %+v", p)
			}
		})
	}
}

func TestPreparationPlanLimitsAndReferences(t *testing.T) {
	for _, reference := range []string{"active", "committed", "target", "prepared", "application", "unknown"} {
		t.Run(reference, func(t *testing.T) {
			in, now := preparationFixture()
			in.Observation.Files[1].References = []string{reference}
			p := PlanPreparation(in, now)
			if len(p.Candidates) != 0 || p.SpaceSatisfied {
				t.Fatal("selected protected file")
			}
		})
	}
	in, now := preparationFixture()
	in.Limits.MaxBytes = 99
	if p := PlanPreparation(in, now); len(p.Candidates) != 0 || len(p.Blockers) == 0 {
		t.Fatal("exceeded byte limit")
	}
	in, now = preparationFixture()
	in.Observation.RequiredFreeBytes = 300
	if p := PlanPreparation(in, now); p.SpaceSatisfied || len(p.Candidates) != 1 || len(p.Blockers) == 0 {
		t.Fatal("claimed insufficient plan met space threshold")
	}
	in, now = preparationFixture()
	in.Observation.FreeBytes = 1000
	if p := PlanPreparation(in, now); !p.SpaceSatisfied || len(p.Candidates) != 0 {
		t.Fatal("unnecessary cleanup")
	}
	in, now = preparationFixture()
	in.Observation.Files[1].Kind = "DiagnosticArchive"
	if p := PlanPreparation(in, now); len(p.Candidates) != 0 {
		t.Fatal("selected non-image")
	}
}

func TestPreparationPlanInvalidation(t *testing.T) {
	for name, change := range map[string]func(*PreparationPlanInput){
		"device":      func(i *PreparationPlanInput) { i.Observation.DeviceUID = "recreated" },
		"controller":  func(i *PreparationPlanInput) { i.Observation.ControllerUID = "recreated" },
		"target":      func(i *PreparationPlanInput) { i.Observation.TargetImageID = "different" },
		"policy":      func(i *PreparationPlanInput) { i.Observation.PolicyGeneration++ },
		"revision":    func(i *PreparationPlanInput) { i.Observation.Revision = "changed" },
		"digest":      func(i *PreparationPlanInput) { i.Observation.Files[1].SHA256 = strings.Repeat("c", 64) },
		"reference":   func(i *PreparationPlanInput) { i.Observation.Files[1].References = []string{"prepared"} },
		"limits":      func(i *PreparationPlanInput) { i.Limits.MaxBytes++ },
		"observation": func(i *PreparationPlanInput) { i.Observation.ObservedAt = i.Observation.ObservedAt.Add(time.Second) },
	} {
		t.Run(name, func(t *testing.T) {
			in, now := preparationFixture()
			p := PlanPreparation(in, now)
			change(&in)
			if ValidatePreparationPlan(p.InputSHA256, in, now) == nil {
				t.Fatal("accepted changed evidence")
			}
		})
	}
	in, now := preparationFixture()
	p := PlanPreparation(in, now)
	if ValidatePreparationPlan(p.InputSHA256, in, now.Add(time.Hour)) == nil {
		t.Fatal("accepted expired plan")
	}
}
