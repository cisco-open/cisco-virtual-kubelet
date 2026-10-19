// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package softwarelifecycle

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// PreparationObservation is evidence supplied by an observer, NOT mutation
// authority. The offline planner cannot authenticate it. A future executor must
// independently obtain these facts through the device worker under admission.
// Paths are canonical platform paths; aliases must be resolved by the observer.
type PreparationObservation struct {
	DeviceUID        string    `json:"deviceUID"`
	PhysicalIdentity string    `json:"physicalIdentity"`
	ControllerUID    string    `json:"controllerUID"`
	TargetImageID    string    `json:"targetImageID"`
	PolicyUID        string    `json:"policyUID"`
	PolicyGeneration int64     `json:"policyGeneration"`
	ObservedAt       time.Time `json:"observedAt"`
	// Revision must change on any inventory, reference or preparation change.
	Revision          string            `json:"revision"`
	Volume            string            `json:"volume"`
	FreeBytes         uint64            `json:"freeBytes"`
	RequiredFreeBytes uint64            `json:"requiredFreeBytes"`
	Complete          map[string]bool   `json:"complete"`
	Quiescent         bool              `json:"quiescent"`
	Files             []PreparationFile `json:"files"`
	// Unresolved includes receipts whose consumption/retirement is unproved.
	Unresolved []string `json:"unresolved"`
}

// Every domain must be positively observed. Empty lists alone prove nothing.
var preparationDomains = []string{"filesystem", "install", "boot", "target", "rollback", "preparations", "applications"}

type PreparationFile struct {
	Path   string `json:"path"`
	Size   uint64 `json:"size"`
	SHA256 string `json:"sha256"`
	// Only ImageArchive is eligible; packages, config and diagnostics are excluded.
	Kind string `json:"kind"`
	// References contains any live protection, including active/committed/boot,
	// target, retained rollback, application or prepared-image references.
	References []string `json:"references"`
	// RetainedRollbackImage identifies a distinct retained rollback image.
	RetainedRollbackImage string `json:"retainedRollbackImage,omitempty"`
}

type PreparationLimits struct {
	MaxFiles               int    `json:"maxFiles"`
	MaxBytes               uint64 `json:"maxBytes"`
	HeadroomBytes          uint64 `json:"headroomBytes"`
	PreserveRollbackImages int    `json:"preserveRollbackImages"`
	MaxAgeSeconds          int64  `json:"maxAgeSeconds"`
}

type PreparationPlanInput struct {
	Observation PreparationObservation `json:"observation"`
	Limits      PreparationLimits      `json:"limits"`
}

type PreparationFileDecision struct {
	Path        string `json:"path"`
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
}

// PreparationPlan is always PlanOnly. Candidates are estimates for review and
// never a FileRemove request, grant, or permission to advance SWIM.
type PreparationPlan struct {
	Mode                    string                    `json:"mode"`
	InputSHA256             string                    `json:"inputSHA256"`
	SpaceSatisfied          bool                      `json:"spaceSatisfied"`
	RequiredFreeBytes       uint64                    `json:"requiredFreeBytes"`
	EstimatedReclaimedBytes uint64                    `json:"estimatedReclaimedBytes"`
	Candidates              []PreparationFile         `json:"candidates"`
	Decisions               []PreparationFileDecision `json:"decisions"`
	Blockers                []string                  `json:"blockers"`
}

// PlanPreparation fails closed on partial/stale/ambiguous evidence. Selection
// is deterministic by canonical path and bounded by BOTH policy limits. It
// does not promise an optimal subset, nor equate estimated size with freed space.
func PlanPreparation(in PreparationPlanInput, now time.Time) PreparationPlan {
	p := PreparationPlan{Mode: "PlanOnly", Candidates: []PreparationFile{}, Decisions: []PreparationFileDecision{}, Blockers: []string{}}
	o, l := in.Observation, in.Limits
	block := func(s string) { p.Blockers = append(p.Blockers, s) }
	if o.DeviceUID == "" || o.PhysicalIdentity == "" || o.ControllerUID == "" || o.TargetImageID == "" || o.PolicyUID == "" || o.PolicyGeneration < 1 || o.Revision == "" {
		block("missing immutable device/controller/image/policy/evidence binding")
	}
	if l.MaxFiles < 1 || l.MaxBytes == 0 || l.PreserveRollbackImages < 1 || l.MaxAgeSeconds < 1 || l.MaxAgeSeconds > 3600 {
		block("invalid limits: positive deletion bounds, rollback retention and freshness <= 3600 seconds required")
	}
	if o.ObservedAt.IsZero() || o.ObservedAt.After(now) || l.MaxAgeSeconds < 1 || now.Sub(o.ObservedAt).Seconds() > float64(l.MaxAgeSeconds) {
		block("inventory evidence is missing, stale or future-dated")
	}
	if !o.Quiescent {
		block("device install/preparation state is not quiescent")
	}
	for _, d := range preparationDomains {
		if !o.Complete[d] {
			block("incomplete " + d + " evidence")
		}
	}
	if len(o.Unresolved) != 0 {
		block("unresolved references or preparation receipts")
	}
	if o.Volume != "flash:" && o.Volume != "bootflash:" {
		block("unsupported image volume")
	}
	if o.RequiredFreeBytes == 0 || ^uint64(0)-o.RequiredFreeBytes < l.HeadroomBytes {
		block("missing or overflowing required space")
	} else {
		p.RequiredFreeBytes = o.RequiredFreeBytes + l.HeadroomBytes
	}
	// Copy before sorting: planning must not mutate caller-owned evidence.
	o.Files = append([]PreparationFile(nil), o.Files...)
	for i := range o.Files {
		o.Files[i].References = append([]string(nil), o.Files[i].References...)
		sort.Strings(o.Files[i].References)
	}
	sort.Slice(o.Files, func(i, j int) bool { return o.Files[i].Path < o.Files[j].Path })
	o.Unresolved = append([]string(nil), o.Unresolved...)
	sort.Strings(o.Unresolved)
	seen, rollback := map[string]bool{}, map[string]bool{}
	for _, f := range o.Files {
		if !canonicalPreparationPath(o.Volume, f.Path) || seen[f.Path] {
			block("invalid or duplicate canonical file path")
		}
		seen[f.Path] = true
		if f.RetainedRollbackImage != "" {
			rollback[f.RetainedRollbackImage] = true
		}
		if f.Kind == "ImageArchive" && (!validPreparationDigest(f.SHA256) || f.Size == 0) {
			block("image archive lacks exact size or SHA-256")
		}
	}
	if len(rollback) < l.PreserveRollbackImages {
		block("insufficient positively identified retained rollback images")
	}
	in.Observation = o
	encoded, err := json.Marshal(in)
	if err != nil {
		block("evidence cannot be encoded")
	} else {
		sum := sha256.Sum256(encoded)
		p.InputSHA256 = hex.EncodeToString(sum[:])
	}
	for _, f := range o.Files {
		d := PreparationFileDecision{Path: f.Path}
		switch {
		case len(f.References) > 0 || f.RetainedRollbackImage != "":
			d.Disposition, d.Reason = "Protected", "live reference or retained rollback image"
		case f.Kind != "ImageArchive" || !strings.HasSuffix(f.Path, ".bin"):
			d.Disposition, d.Reason = "Excluded", "only unreferenced image archives are eligible"
		case len(p.Blockers) > 0:
			d.Disposition, d.Reason = "Unknown", "inventory or policy is not sufficient to plan cleanup"
		case o.FreeBytes >= p.RequiredFreeBytes || p.EstimatedReclaimedBytes >= p.RequiredFreeBytes-o.FreeBytes:
			d.Disposition, d.Reason = "Retained", "space threshold already covered"
		case len(p.Candidates) >= l.MaxFiles || f.Size > l.MaxBytes-p.EstimatedReclaimedBytes:
			d.Disposition, d.Reason = "Excluded", "policy deletion bound"
		default:
			d.Disposition, d.Reason = "Candidate", "unreferenced archive within supplied policy bounds; observation only"
			p.Candidates = append(p.Candidates, f)
			p.EstimatedReclaimedBytes += f.Size
		}
		p.Decisions = append(p.Decisions, d)
	}
	if len(p.Blockers) == 0 {
		p.SpaceSatisfied = o.FreeBytes >= p.RequiredFreeBytes || p.EstimatedReclaimedBytes >= p.RequiredFreeBytes-o.FreeBytes
		if !p.SpaceSatisfied {
			block("eligible files within policy bounds cannot meet required space")
		}
	}
	return p
}

// ValidatePreparationPlan compares against newly observed facts, including
// reference revision and observation timestamp. It is intentionally strict:
// any new observation requires a new plan. It does NOT authorize a mutation.
func ValidatePreparationPlan(expectedSHA256 string, fresh PreparationPlanInput, now time.Time) error {
	p := PlanPreparation(fresh, now)
	if !validPreparationDigest(expectedSHA256) || p.InputSHA256 != expectedSHA256 {
		return fmt.Errorf("preparation evidence or policy changed; replan required")
	}
	if len(p.Blockers) != 0 {
		return fmt.Errorf("preparation blocked: %s", strings.Join(p.Blockers, "; "))
	}
	return nil
}

func validPreparationDigest(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}

func canonicalPreparationPath(volume, path string) bool {
	if !strings.HasPrefix(path, volume) || volume == "" {
		return false
	}
	rest := strings.TrimPrefix(path, volume)
	if rest == "" {
		return false
	}
	for _, part := range strings.Split(rest, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("._-", c)) {
				return false
			}
		}
	}
	return true
}
