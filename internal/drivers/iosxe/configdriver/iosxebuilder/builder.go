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
// the set of release tags as a closed validator (set semantics)
// plus the default tag. Failure to load is non-fatal; we log via
// the context-bound logger and return empty maps so
// spec.targetYangVersion validation simply disables.
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
		supported[r.Version] = struct{}{}
		if r.Default {
			def = r.Version
		}
	}
	return supported, def
}

// LookupWriter is the platform-scoped writer-registry accessor
// the platform-agnostic ConfigReconciler needs. Wraps writers.Get
// so the registry stays an internal detail of the iosxe package.
func LookupWriter(family string) writers.SectionWriter {
	return writers.Get(family)
}

// UnionWriterPaths returns the sorted union of YANG paths every
// registered IOS-XE writer touches. The gNMI Subscribe watcher
// uses this set so an out-of-band write to any leaf the engine
// cares about triggers an off-cycle reconcile.
func UnionWriterPaths() []string {
	seen := map[string]struct{}{}
	for _, fam := range writers.Families() {
		w := writers.Get(fam)
		if w == nil {
			continue
		}
		for _, p := range w.YANGPaths() {
			seen[p] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
