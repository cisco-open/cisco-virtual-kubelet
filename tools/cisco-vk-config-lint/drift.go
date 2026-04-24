// Copyright © 2026 Cisco Systems, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

// Report is the structured output of one lint run against a device.
// Two dimensions per the reviewer's feedback:
//
//   - ManagedDrift: families claimed by at least one IOSXEConfig CR
//     whose device state has diverged from the declared intent. The
//     operator sees "what CVK would change on the next reconcile".
//
//   - Orphans: registered families whose device state is non-empty
//     but which no IOSXEConfig CR claims. The operator sees "what
//     is on the device that CVK will not touch" — typically
//     legitimate (system.hostname on a brownfield device, for
//     example), but sometimes a cutover gap to address.
type Report struct {
	// Device is the target device the report was produced against.
	Device string `json:"device"`

	// ManagedFamilies is the union of spec.managedFamilies across
	// every IOSXEConfig CR the tool loaded for this device. Included
	// so JSON consumers can correlate orphans against the claim set.
	ManagedFamilies []string `json:"managedFamilies"`

	// ManagedDrift reports per-family drift for families in
	// ManagedFamilies. A family is listed only when its writer's
	// Diff returned non-empty ops against the live device.
	ManagedDrift []FamilyDrift `json:"managedDrift,omitempty"`

	// Orphans lists registered families with device-present state
	// that no CR claims. An empty slice means every non-empty
	// family on the device is accounted for by some IOSXEConfig.
	Orphans []Orphan `json:"orphans,omitempty"`

	// Errors records per-family Fetch/Diff failures. A family here
	// does NOT count toward ManagedDrift or Orphans — the tool
	// cannot make a drift claim without a clean read.
	Errors []FamilyError `json:"errors,omitempty"`
}

// FamilyDrift describes what the writer's Diff would apply for one
// managed family. The op count is the "blast radius" indicator; the
// verbs histogram lets operators gate on the kind of change
// ("fail CI on any DELETE" is a common guardrail).
type FamilyDrift struct {
	Family   string         `json:"family"`
	OpCount  int            `json:"opCount"`
	Verbs    map[string]int `json:"verbs,omitempty"`
	Claimers []string       `json:"claimers"`
}

// Orphan describes a device-present family with no CR claim. The
// YANG paths let a reviewer jump from the report to the device's
// actual configuration without cross-referencing families.yaml.
type Orphan struct {
	Family    string   `json:"family"`
	YANGPaths []string `json:"yangPaths,omitempty"`
}

// FamilyError captures a per-family Fetch/Diff failure.
type FamilyError struct {
	Family string `json:"family"`
	Stage  string `json:"stage"` // "fetch" | "diff"
	Err    string `json:"error"`
}

// driftInputs captures the per-family work unit for one lint run.
type driftInputs struct {
	device   string
	claimers map[string][]string // family -> CRs that include it in managedFamilies
	intents  map[string]any      // family -> resolved desired state from the union of CRs
}

// computeReport runs the two-dimension drift check against a live
// device. It is the testable heart of the lint tool; main.go is a
// thin CLI wrapper around this.
//
// The tool does NOT perform the full scope resolution the engine
// does — templates, defaults, and device groups are expected to be
// materialised into the IOSXEConfig CRs' spec.source.inline body at
// the lint site (either via GitOps pre-render or by checking in the
// resolved CRs). This keeps the tool a pure drift reporter and
// avoids dragging controller-runtime into the dependency graph.
func computeReport(
	ctx context.Context,
	t transport.Interface,
	inputs driftInputs,
	ignored map[string]struct{},
) Report {
	report := Report{
		Device:          inputs.device,
		ManagedFamilies: sortedKeys(inputs.claimers),
	}

	// Iterate every registered family in a stable order so the
	// report is diff-friendly.
	for _, family := range writers.Families() {
		if _, skip := ignored[family]; skip {
			continue
		}
		w := writers.Get(family)
		if w == nil {
			// Unreachable: Families() returns only registered writers.
			continue
		}

		observed, err := w.Fetch(ctx, t)
		if err != nil {
			report.Errors = append(report.Errors, FamilyError{
				Family: family, Stage: "fetch", Err: err.Error(),
			})
			continue
		}

		claimers, claimed := inputs.claimers[family]
		if claimed {
			ops, err := w.Diff(inputs.intents[family], observed)
			if err != nil {
				report.Errors = append(report.Errors, FamilyError{
					Family: family, Stage: "diff", Err: err.Error(),
				})
				continue
			}
			if len(ops) > 0 {
				report.ManagedDrift = append(report.ManagedDrift, FamilyDrift{
					Family:   family,
					OpCount:  len(ops),
					Verbs:    countVerbs(ops),
					Claimers: append([]string(nil), claimers...),
				})
			}
			continue
		}

		if isObservedNonEmpty(observed) {
			report.Orphans = append(report.Orphans, Orphan{
				Family:    family,
				YANGPaths: w.YANGPaths(),
			})
		}
	}
	return report
}

// countVerbs tallies the verbs in an op slice. Map is omitted from
// JSON when empty (see the omitempty tag on FamilyDrift.Verbs) so a
// no-diff family doesn't render a pointless empty object.
func countVerbs(ops []transport.Op) map[string]int {
	out := map[string]int{}
	for _, op := range ops {
		out[string(op.Verb)]++
	}
	return out
}

// isObservedNonEmpty distinguishes "family has state on the device"
// from "family is absent / 404'd to empty". Writers report observed
// state in three shapes — map (singleton), list, or nil — so the
// test is three-way.
func isObservedNonEmpty(observed any) bool {
	switch v := observed.(type) {
	case nil:
		return false
	case map[string]any:
		return len(v) > 0
	case []map[string]any:
		return len(v) > 0
	case []any:
		return len(v) > 0
	default:
		// Scalar-like shape — treat as present; writers don't produce
		// scalars at this level today, but future writers might.
		return true
	}
}

// sortedKeys returns the keys of m sorted lexicographically so
// report slices stay deterministic across runs.
func sortedKeys(m map[string][]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// HasFindings reports whether the report contains drift or orphans.
// Errors do NOT count — an unreachable family isn't "drift", it's a
// tool-level failure the operator should investigate separately.
func (r *Report) HasFindings() bool {
	return len(r.ManagedDrift) > 0 || len(r.Orphans) > 0
}

// computeOfflinePlan is the no-device counterpart of computeReport.
// Each managed family's writer is invoked with observed=nil, so the
// returned ops are "everything the engine would push if the device
// were empty". Useful in two regulated-environment workflows:
//
//   - Greenfield review: confirm an IOSXEConfig CR resolves to the
//     expected vocabulary of ops before any device write.
//   - GitOps gating: a PR pipeline that has no device access can
//     still rule on whether a manifest change is non-trivial.
//
// Orphan detection requires a device read and is therefore omitted.
// Operators who need orphans run the connected report; the lint
// flag layer enforces this combination at parse time.
func computeOfflinePlan(inputs driftInputs, ignored map[string]struct{}) Report {
	report := Report{
		Device:          inputs.device,
		ManagedFamilies: sortedKeys(inputs.claimers),
	}
	for _, family := range writers.Families() {
		if _, skip := ignored[family]; skip {
			continue
		}
		claimers, claimed := inputs.claimers[family]
		if !claimed {
			continue
		}
		w := writers.Get(family)
		if w == nil {
			continue
		}
		ops, err := w.Diff(inputs.intents[family], nil)
		if err != nil {
			report.Errors = append(report.Errors, FamilyError{
				Family: family, Stage: "diff", Err: err.Error(),
			})
			continue
		}
		if len(ops) > 0 {
			report.ManagedDrift = append(report.ManagedDrift, FamilyDrift{
				Family:   family,
				OpCount:  len(ops),
				Verbs:    countVerbs(ops),
				Claimers: append([]string(nil), claimers...),
			})
		}
	}
	return report
}

// Summary is a compact one-line rendering suited to CI log tails.
func (r *Report) Summary() string {
	return fmt.Sprintf(
		"device=%s managed=%d drifted=%d orphans=%d errors=%d",
		r.Device, len(r.ManagedFamilies),
		len(r.ManagedDrift), len(r.Orphans), len(r.Errors),
	)
}
