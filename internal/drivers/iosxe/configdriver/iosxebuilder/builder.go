// Copyright © 2026 Cisco Systems Inc.
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

package iosxebuilder

// This file holds the per-platform helpers the cisco-vk binary
// (and the aggregator) used to import directly:
//
//   - keyRulesForPhase1() - IOS-XE family-specific key rules
//   - LoadYANGReleaseTags() - validator set + default release for
//     spec.targetYangVersion
//   - UnionWriterPaths() - YANG-path set the gNMI Subscribe watcher
//     should subscribe to
//   - LookupWriter / Writers - re-exports of the iosxe writers
//     registry's Get / Families functions, so the platform's
//     reconciler-builder hands a stable function pointer to the
//     platform-agnostic registry without exposing its writers
//     package
//
// The Phase-9 plug-in registry calls into these from
// `internal/drivers/iosxe/register.go::buildIOSXEConfigDriverContext`.
// Adding a new platform is the symmetric exercise: a new
// `internal/drivers/<platform>/configdriver/builder.go` exposes
// the same function set with platform-specific contents.

import (
	"context"
	"sort"

	"github.com/virtual-kubelet/virtual-kubelet/log"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/intent"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/schema"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
)

// KeyRulesForXE returns the path → key-field rules the merger
// uses for YANG-keyed lists in the IOS-XE family set. New
// keyed-list families should add an entry here so MergeWithRules
// keys them by the right leaf.
func KeyRulesForXE() intent.KeyRules {
	return intent.KeyRules{
		"vlan.vlans":                              "id",
		"vrf.vrfs":                                "name",
		"interface_ethernet.interfaces":           "name",
		"interface_loopback.interfaces":           "name",
		"interface_virtual_port_group.interfaces": "id",
		"dhcp.pools":                              "name",
		"access_list_extended.extended":           "name",
	}
}

// xeGNMIPathKeys is the path-segment → list-key mapping the gNMI
// transport uses to disambiguate keyed paths under parseGNMIPath's
// fallback. Authored alongside KeyRulesForXE so adding a new
// keyed-list family touches one place per concern. Each entry
// matches LastPathSegment(yangPath).
//
// Wave 5A-fu (external-review-followup Finding #4): Wave 5A
// populated this registry as a side effect of schema.LoadFamilies.
// Production startup never calls LoadFamilies — only the docs
// generator does — so the Wave 5A registry was a no-op in the
// running cisco-vk binary. The fix wires registration through
// iosxebuilder which IS on the production startup path
// (KeyRulesForXE is consumed by every binary that builds the
// IOS-XE config-driver context, both per-pod and aggregator).
var xeGNMIPathKeys = map[string][]string{
	// /Cisco-IOS-XE-native:native/vlan/Cisco-IOS-XE-vlan:vlan-list
	"vlan-list": {"id"},
	// /Cisco-IOS-XE-native:native/vrf/definition
	"definition": {"name"},
	// Concrete interface paths each carry the type as a path
	// segment, so the gNMI list key is just `name`.
	"GigabitEthernet":      {"name"},
	"TwoGigabitEthernet":   {"name"},
	"FiveGigabitEthernet":  {"name"},
	"TenGigabitEthernet":   {"name"},
	"TwentyFiveGigE":       {"name"},
	"FortyGigabitEthernet": {"name"},
	"HundredGigE":          {"name"},
	"TwoHundredGigE":       {"name"},
	"FourHundredGigE":      {"name"},
	"Loopback":             {"name"},
	"VirtualPortGroup":     {"name"},
	"Tunnel":               {"name"},
	"Vlan":                 {"name"},
	"Port-channel":         {"name"},
	// /Cisco-IOS-XE-native:native/ip/dhcp/pool
	"pool": {"name"},
	// /Cisco-IOS-XE-native:native/ip/access-list/extended
	"extended": {"name"},
	"standard": {"name"},
}

// RegisterGNMIPathKeysForXE wires the IOS-XE keyed-list registry
// into the transport package. Called from
// internal/drivers/iosxe/register.go's init or the platform
// builder so every binary linking the iosxe driver gets the
// registrations — production cisco-vk, the aggregator, and the
// lint tool when it links against this package.
//
// Idempotent. Calling it from multiple sites (init + explicit
// startup) is safe; transport.RegisterPathKey is a sync.Once-style
// upsert.
func RegisterGNMIPathKeysForXE() {
	for seg, keys := range xeGNMIPathKeys {
		transport.RegisterPathKey(seg, keys...)
	}
}

// LoadYANGReleaseTags reads schema/yang-versions.yaml and returns
// the set of release tags valid for spec.targetYangVersion plus the
// default tag.
//
// The set honours the per-release `status:` field:
//
//	supported    → enters the validator set (default-eligible)
//	experimental → enters the validator set with a logged warning
//	deprecated   → enters the validator set with a logged warning
//	(anything else, including missing) → REJECTED with a logged warning
//
// "Supported" alone would block experimental rollouts; "every status"
// would silently accept releases the team has explicitly disowned.
// The middle path mirrors how operators actually use the file:
// experimental for soak, deprecated for one-release-warning before
// removal, unknown for typos that should fail the load loudly.
//
// Failure to load is non-fatal; we log via the context-bound logger
// and return empty maps so spec.targetYangVersion validation simply
// disables — preferable to a hard startup abort when the cisco-vk
// pod has otherwise come up clean.
func LoadYANGReleaseTags(ctx context.Context) (map[string]struct{}, string) {
	logger := log.G(ctx).WithField("component", "iosxe-configdriver")
	releases, err := schema.LoadYANGReleases()
	if err != nil {
		logger.WithError(err).
			Warn("could not load yang-versions.yaml; spec.targetYangVersion validation disabled")
		return nil, ""
	}
	supported := make(map[string]struct{}, len(releases))
	var def string
	for _, r := range releases {
		entryLog := logger.WithField("release", r.Version).WithField("status", r.Status)
		switch r.Status {
		case "supported":
			supported[r.Version] = struct{}{}
		case "experimental":
			supported[r.Version] = struct{}{}
			entryLog.Warn("YANG release is marked experimental; integration coverage may be incomplete")
		case "deprecated":
			supported[r.Version] = struct{}{}
			entryLog.Warn("YANG release is deprecated; remove from yang-versions.yaml on the next CVK release")
		default:
			// Unknown status (typo, missing) — refuse to admit it.
			// A loud warning beats silent acceptance of a release the
			// team didn't intentionally bless.
			entryLog.Warn("YANG release has unknown status; rejecting from validator set")
			continue
		}
		if r.Default {
			if r.Status != "supported" {
				entryLog.Warn("YANG release marked default but status != supported; honouring default to avoid breaking existing CRs")
			}
			def = r.Version
		}
	}
	return supported, def
}

// LookupWriter is the platform-scoped writer-registry accessor
// the platform-agnostic ConfigReconciler needs. Wraps writers.Get
// so the registry stays an internal detail of the iosxe package.
func LookupWriter(family, release string) writers.SectionWriter {
	return writers.GetForRelease(family, release)
}

// FamilyOrderForXE returns a function suitable for
// engine.Engine.FamilyOrder: a topological sort of any input
// family slice using the schema's depends_on declarations as the
// dependency edges. Parents come before dependents so add-set
// operations are sequenced correctly during atomic replace
// (Wave 10.3) — e.g. a VRF write runs before any interface_*
// write that binds to it.
//
// Behaviour with cycles, missing-from-schema entries, or unknown
// families: the function falls back to lexicographic order over
// the original input so reconcile remains deterministic. Cycles
// in the schema are a static-validation bug (covered separately
// by schema/index_test.go) and should never reach this code in
// production.
//
// Returns nil if schema.LoadFamilies fails — the caller (the
// engine) treats nil as identity ordering, preserving the
// pre-Wave-10 behaviour. This is the safe failure mode: a
// schema-load problem should not break reconciles, just fall back
// to operator-given ordering with no atomic-replace
// cross-family ordering benefit.
func FamilyOrderForXE() func([]string) []string {
	families, err := schema.LoadFamilies()
	if err != nil {
		return nil
	}
	return func(in []string) []string {
		// Bucketize: families known to the schema get topo-sorted;
		// unknown families append at the end in input order.
		known := make(map[string]bool, len(in))
		for _, f := range in {
			if _, ok := families[f]; ok {
				known[f] = true
			}
		}
		// Topo sort with deterministic tie-breaking on lexicographic
		// order. Kahn's algorithm; cycle detection falls through to
		// lex-order over the original input (defensive — the schema
		// should be cycle-free per its own validation).
		indeg := make(map[string]int, len(known))
		for f := range known {
			indeg[f] = 0
		}
		for f := range known {
			for _, dep := range families[f].DependsOn {
				if known[dep] {
					indeg[f]++
				}
			}
		}
		var ready []string
		for f, d := range indeg {
			if d == 0 {
				ready = append(ready, f)
			}
		}
		sort.Strings(ready)
		out := make([]string, 0, len(in))
		for len(ready) > 0 {
			next := ready[0]
			ready = ready[1:]
			out = append(out, next)
			// Visit families that depend on `next` and decrement.
			for f := range known {
				for _, dep := range families[f].DependsOn {
					if dep == next {
						indeg[f]--
						if indeg[f] == 0 {
							ready = append(ready, f)
						}
					}
				}
			}
			sort.Strings(ready)
		}
		// Cycle detection: if any indeg > 0 remain, fall back to
		// lex order over the original input slice.
		for _, d := range indeg {
			if d > 0 {
				lex := append([]string(nil), in...)
				sort.Strings(lex)
				return lex
			}
		}
		// Append unknown-to-schema families in input order so the
		// engine still processes them.
		for _, f := range in {
			if !known[f] {
				out = append(out, f)
			}
		}
		return out
	}
}

// UnionWriterPaths returns the sorted union of YANG paths every
// registered IOS-XE writer touches. The gNMI Subscribe watcher
// uses this set so an out-of-band write to any leaf the engine
// cares about triggers an off-cycle reconcile.
func UnionWriterPaths() []string {
	seen := map[string]struct{}{}
	for _, fam := range writers.Families() {
		w := writers.GetForRelease(fam, "")
		if w == nil {
			continue
		}
		for _, p := range w.YANGPaths() {
			seen[p] = struct{}{}
		}
		for _, version := range writers.SupportedDeviceVersions() {
			w := writers.GetForRelease(fam, version)
			if w == nil {
				continue
			}
			for _, p := range w.YANGPaths() {
				seen[p] = struct{}{}
			}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
