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

package writers

import (
	"context"
	"sort"

	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// SNMP server Phase-2 writer.
//
// netascode (common subset):
//
//   snmp_server:
//     community:
//       - name: public
//         access: ro
//     location: "colo-1"
//     contact: "noc@example.com"
//     trap_source_interface:
//       Loopback: "0"
//
// YANG: /Cisco-IOS-XE-native:native/snmp-server. Phase-2 manages the
// commonly-configured leaves; v3 groups/users and engine-id
// management are Phase-3.
//
// The SNMP community list uses empty leaves `RO` and `RW` to
// encode the access level — YANG `type empty;` not a string.
// Caught against C8000V 17.16.01a: {"community":[{"name":"public",
// "access":"ro"}]} rejected with malformed-message.

func init() {
	Override(snmpWriter{})
}

type snmpWriter struct {
	resolver *OverrideResolver
}

func (snmpWriter) Family() string      { return "snmp_server" }
func (snmpWriter) YANGPaths() []string { return []string{"/Cisco-IOS-XE-native:native/snmp-server"} }

func (w snmpWriter) withResolver(r *OverrideResolver) SectionWriter {
	w.resolver = r
	return w
}

func (w snmpWriter) resolverForUse() *OverrideResolver {
	return ensureResolver(w.resolver)
}

func (w snmpWriter) Fetch(ctx context.Context, c transport.Interface) (any, error) {
	resolver := w.resolverForUse()
	envKey := resolver.ResolvedEnvelopeKey("snmp_server", "Cisco-IOS-XE-snmp:snmp-server")
	sw := singletonWriter{
		family:        "snmp_server",
		yangPath:      "/Cisco-IOS-XE-native:native/snmp-server",
		envelopeKey:   envKey,
		managedLeaves: snmpManagedLeaves,
		resolver:      resolver,
	}
	observed, err := sw.Fetch(ctx, c)
	if err != nil {
		return nil, err
	}
	m, _ := observed.(map[string]any)
	if m == nil {
		return observed, nil
	}
	// Reverse version-conditional element renames (e.g. Cisco-IOS-XE-snmp:contact → contact).
	if o, ok := resolver.GetOverride("snmp_server"); ok {
		m = ReverseElementMap(m, o.ElementMap)
	}
	// Normalise community entries: YANG RO/RW → netascode access (17.18),
	// or community-config[{permission}] → community[{access}] (17.16).
	if comms, ok := m["community"].([]any); ok {
		for i, c := range comms {
			entry, ok := c.(map[string]any)
			if !ok {
				continue
			}
			comms[i] = snmpCommunityFromYANG(entry)
		}
	}
	// 17.16: community-config → community (reverse of the BodyTransform).
	if configs, ok := m["Cisco-IOS-XE-snmp:community-config"].([]any); ok {
		comms := make([]any, 0, len(configs))
		for _, c := range configs {
			entry, ok := c.(map[string]any)
			if !ok {
				continue
			}
			out := map[string]any{}
			if n, ok := entry["name"]; ok {
				out["name"] = n
			}
			if p, ok := entry["permission"].(string); ok {
				out["access"] = p
			}
			comms = append(comms, out)
		}
		m["community"] = comms
		delete(m, "Cisco-IOS-XE-snmp:community-config")
	}
	// Sort community list by name for stable comparison (device may
	// return entries in a different order than desired).
	snmpSortCommunities(m)
	return m, nil
}

func (w snmpWriter) Diff(desired, observed any) ([]transport.Op, error) {
	desiredMap, err := coerceMap(desired, "snmp_server.desired")
	if err != nil {
		return nil, err
	}
	observedMap, err := coerceMap(observed, "snmp_server.observed")
	if err != nil {
		return nil, err
	}
	if desiredMap == nil {
		return nil, nil
	}
	if observedMap == nil {
		observedMap = map[string]any{}
	}
	// Normalise community order on both sides before comparison so
	// that device-side ordering differences don't cause drift loops.
	snmpSortCommunities(desiredMap)
	snmpSortCommunities(observedMap)
	// Community uses set-membership semantics: every desired community must
	// exist and match in the observed set; extra observed communities are not
	// drift because the writer uses MERGE and cannot remove them.
	// All other managed leaves use the standard leavesEqual check.
	nonCommLeaves := make([]string, 0, len(snmpManagedLeaves))
	for _, l := range snmpManagedLeaves {
		if l != "community" && l != "community-config" && l != "Cisco-IOS-XE-snmp:community-config" {
			nonCommLeaves = append(nonCommLeaves, l)
		}
	}
	if !leavesEqual(desiredMap, observedMap, nonCommLeaves) {
		// Non-community leaf differs — fall through to emit op.
	} else if snmpCommunitiesMatch(desiredMap, observedMap) {
		return nil, nil
	}
	proj := projectManagedLeaves(desiredMap, snmpManagedLeaves)
	// Transform community entries for YANG wire shape.
	if comms, ok := proj["community"].([]any); ok {
		fixed := make([]any, 0, len(comms))
		for _, c := range comms {
			entry, ok := c.(map[string]any)
			if !ok {
				fixed = append(fixed, c)
				continue
			}
			fixed = append(fixed, snmpCommunityToYANG(entry))
		}
		proj["community"] = fixed
	}
	// Apply version-conditional overrides (module prefix renames).
	resolver := w.resolverForUse()
	if o, ok := resolver.GetOverride("snmp_server"); ok {
		proj = ApplyOverrideToBody(proj, o)
	}
	envKey := resolver.ResolvedEnvelopeKey("snmp_server", "Cisco-IOS-XE-snmp:snmp-server")
	body, err := wrapYANGPayload(envKey, proj)
	if err != nil {
		return nil, err
	}
	return []transport.Op{{
		Verb: transport.VerbMerge,
		Path: "/Cisco-IOS-XE-native:native/snmp-server",
		Body: body,
	}}, nil
}

func (w snmpWriter) Apply(ctx context.Context, c transport.Interface, ops []transport.Op) error {
	if len(ops) == 0 {
		return nil
	}
	return c.Mutate(ctx, "", ops)
}

var snmpManagedLeaves = []string{
	"community",
	"community-config",
	"Cisco-IOS-XE-snmp:community-config",
	"location",
	"contact",
	"Cisco-IOS-XE-snmp:contact",
	"Cisco-IOS-XE-snmp:location",
	"trap-source",
	"host",
}

// snmpCommunityToYANG transforms the netascode community entry to
// the YANG shape: access: "ro" → RO: [null], access: "rw" → RW: [null].
func snmpCommunityToYANG(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		if k == "access" {
			s, _ := v.(string)
			switch s {
			case "ro", "RO":
				out["RO"] = []any{nil}
			case "rw", "RW":
				out["RW"] = []any{nil}
			}
			continue
		}
		out[k] = v
	}
	return out
}

// snmpBodyTransform1716 converts the 17.18 community shape
// (community[{name, RO:[null]}]) to the 17.16 shape
// (Cisco-IOS-XE-snmp:community-config[{name, permission:"ro"}]).
func snmpBodyTransform1716(body map[string]any) map[string]any {
	if comms, ok := body["community"]; ok {
		if arr, ok := comms.([]any); ok {
			configs := make([]any, 0, len(arr))
			for _, c := range arr {
				entry, ok := c.(map[string]any)
				if !ok {
					continue
				}
				cfg := map[string]any{}
				if n, ok := entry["name"]; ok {
					cfg["name"] = n
				}
				if _, ok := entry["RO"]; ok {
					cfg["permission"] = "ro"
				} else if _, ok := entry["RW"]; ok {
					cfg["permission"] = "rw"
				}
				configs = append(configs, cfg)
			}
			body["Cisco-IOS-XE-snmp:community-config"] = configs
		}
		delete(body, "community")
	}
	return body
}

// snmpCommunitiesMatch reports true when every desired community exists
// and matches in the observed set. Extra observed communities are ignored
// (the writer uses MERGE and cannot remove them).
func snmpCommunitiesMatch(desired, observed map[string]any) bool {
	wantList, _ := desired["community"].([]any)
	haveList, _ := observed["community"].([]any)
	// Index observed communities by name for O(1) lookup.
	haveByName := make(map[string]map[string]any, len(haveList))
	for _, e := range haveList {
		if m, ok := e.(map[string]any); ok {
			if n, ok := m["name"].(string); ok {
				haveByName[n] = m
			}
		}
	}
	for _, e := range wantList {
		wm, ok := e.(map[string]any)
		if !ok {
			return false
		}
		name, _ := wm["name"].(string)
		hm, exists := haveByName[name]
		if !exists {
			return false
		}
		// Check every desired field matches observed.
		for k, wv := range wm {
			hv, ok := hm[k]
			if !ok || !scalarEqual(wv, hv) {
				return false
			}
		}
	}
	return true
}

// snmpSortCommunities sorts the community slice in-place by the "name"
// field. IOS-XE may return communities in alphabetical or insertion
// order that differs from the desired list, causing spurious drift.
func snmpSortCommunities(m map[string]any) {
	comms, ok := m["community"].([]any)
	if !ok || len(comms) < 2 {
		return
	}
	sort.SliceStable(comms, func(i, j int) bool {
		a, _ := comms[i].(map[string]any)
		b, _ := comms[j].(map[string]any)
		an, _ := a["name"].(string)
		bn, _ := b["name"].(string)
		return an < bn
	})
}

// snmpCommunityFromYANG inverts the transform for observed state.
func snmpCommunityFromYANG(entry map[string]any) map[string]any {
	out := make(map[string]any, len(entry))
	for k, v := range entry {
		switch k {
		case "RO":
			out["access"] = "ro"
		case "RW":
			out["access"] = "rw"
		default:
			out[k] = v
		}
	}
	return out
}
