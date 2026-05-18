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

// ──────────────────────────────────────────────────────────────────
// Override table: version-conditional YANG behaviour for IOS-XE.
//
// Each entry targets a specific family + version range. Entries are
// evaluated in order; the first match per family wins. Version
// ranges use inclusive MinVersion and exclusive MaxVersion:
//
//     MinVersion: {17, 0}, MaxVersion: {17, 18}
//     means: "17.0 ≤ version < 17.18"
//
// The baseline (17.18+) needs no entries — writers are authored for
// that version. Overrides handle only divergences from baseline.
//
// To add support for a new IOS-XE release:
//   1. Add an entry below for each family that diverges.
//   2. Run unit tests (yang_version_overrides_test.go).
//   3. Run integration tests on a device of that version.
// ──────────────────────────────────────────────────────────────────

// overrideTable is the ordered list of version overrides. Populated
// at package init time; ResolveForVersion iterates it at startup.
var overrideTable = []VersionOverride{

	// ── route_map: IOS-XE < 17.18 ────────────────────────────────
	// Inner container is "route-map-seq" (not "route-map-without-
	// order-seq"), and on < 17.18 the module prefix is mandatory.
	// Key field inside each seq entry is "ordering-seq" (not "seq").
	{
		Family:     "route_map",
		MinVersion: [2]int{17, 0},
		MaxVersion: [2]int{17, 18},
		ElementMap: map[string]string{
			"route-map-without-order-seq": "Cisco-IOS-XE-route-map:route-map-seq",
			"seq":                         "ordering-seq",
		},
		NestedYANGInnerOverride: "Cisco-IOS-XE-route-map:route-map-seq",
	},

	// ── ntp: IOS-XE < 17.18 ──────────────────────────────────────
	// "prefer" is a YANG empty leaf, not boolean.
	{
		Family:      "ntp",
		MinVersion:  [2]int{17, 0},
		MaxVersion:  [2]int{17, 18},
		EmptyLeaves: []string{"prefer"},
	},

	// ── logging: IOS-XE < 17.18 ──────────────────────────────────
	// The loggingWriter already transforms: {buffered: N} →
	// {buffered: {size: N}}. On 17.16, the sub-element under
	// "buffered" is "size-value" (not "size"). No module prefix
	// needed on "buffered" itself (it's in the native module).
	{
		Family:     "logging",
		MinVersion: [2]int{17, 0},
		MaxVersion: [2]int{17, 18},
		BodyTransform: func(body map[string]any) map[string]any {
			// Rename: buffered.size → buffered.size-value
			if buf, ok := body["buffered"].(map[string]any); ok {
				if sz, ok := buf["size"]; ok {
					delete(buf, "size")
					buf["size-value"] = sz
					body["buffered"] = buf
				}
			}
			return body
		},
	},

	// ── snmp_server: IOS-XE < 17.18 ──────────────────────────────
	// On 17.16 the envelope key is "Cisco-IOS-XE-native:snmp-server"
	// (not "Cisco-IOS-XE-snmp:snmp-server"), all sub-elements need
	// the "Cisco-IOS-XE-snmp:" prefix, and the community structure
	// changes from community[{name, RO:[null]}] to
	// community-config[{name, permission:"ro"}].
	{
		Family:              "snmp_server",
		MinVersion:          [2]int{17, 0},
		MaxVersion:          [2]int{17, 18},
		EnvelopeKeyOverride: "Cisco-IOS-XE-native:snmp-server",
		ElementMap: map[string]string{
			"contact":  "Cisco-IOS-XE-snmp:contact",
			"location": "Cisco-IOS-XE-snmp:location",
		},
		BodyTransform: snmpBodyTransform1716,
	},

	// ── access_list_extended: IOS-XE < 17.18 ─────────────────────
	// On < 17.18 the inner list element requires the module prefix.
	// ace-rule wrapper exists on 17.16 (confirmed by device probing).
	{
		Family:     "access_list_extended",
		MinVersion: [2]int{17, 0},
		MaxVersion: [2]int{17, 18},
		ElementMap: map[string]string{
			"access-list-seq-rule": "Cisco-IOS-XE-acl:access-list-seq-rule",
		},
	},

	// ── access_list_standard: IOS-XE < 17.18 ─────────────────────
	// Standard ACL uses permit/deny → std-ace wrapper instead of
	// ace-rule. Body/Fetch shapes handle the conversion. Module
	// prefix is still required on the inner list element.
	{
		Family:     "access_list_standard",
		MinVersion: [2]int{17, 0},
		MaxVersion: [2]int{17, 18},
		ElementMap: map[string]string{
			"access-list-seq-rule": "Cisco-IOS-XE-acl:access-list-seq-rule",
		},
	},

	// ── bgp: IOS-XE < 17.18 ──────────────────────────────────────
	// On 17.16, BGP is a keyed list (Cisco-IOS-XE-bgp:bgp) under
	// /router, not a container (router-bgp). Transform logic lives
	// in the bgpWriter; this entry drives path/envelope selection.
	{
		Family:              "bgp",
		MinVersion:          [2]int{17, 0},
		MaxVersion:          [2]int{17, 18},
		YANGPathOverride:    "/Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-bgp:bgp",
		EnvelopeKeyOverride: "Cisco-IOS-XE-bgp:bgp",
	},

	// ── prefix_list: IOS-XE < 17.18 ──────────────────────────────
	// On 17.16 the YANG path is /ip/prefix-lists (plural) with a
	// flat compound-keyed prefixes[name, no] list, plus a separate
	// prefix-list-description[name] list. Transform logic lives in
	// the prefixListWriter; this entry drives path/envelope selection.
	{
		Family:              "prefix_list",
		MinVersion:          [2]int{17, 0},
		MaxVersion:          [2]int{17, 18},
		YANGPathOverride:    "/Cisco-IOS-XE-native:native/ip/prefix-lists",
		EnvelopeKeyOverride: "Cisco-IOS-XE-native:prefix-lists",
	},

	// ── ip_community_list: IOS-XE < 17.18 ────────────────────────
	// On 17.16 the community-list uses deprecated groupings
	// (permit/deny-list, extended-grouping). Transform logic lives
	// in the communityListWriter. Path/envelope are the same across
	// versions so no path override needed — this entry is a
	// sentinel for IsLegacyVersion().
	{
		Family:     "ip_community_list",
		MinVersion: [2]int{17, 0},
		MaxVersion: [2]int{17, 18},
	},

	// ── spanning_tree: IOS-XE < 17.18 ────────────────────────────
	// On < 17.18 the envelope key is "Cisco-IOS-XE-native:spanning-tree"
	// (not "Cisco-IOS-XE-spanning-tree:spanning-tree"), and
	// augmented leaves need the "Cisco-IOS-XE-spanning-tree:" prefix.
	{
		Family:              "spanning_tree",
		MinVersion:          [2]int{17, 0},
		MaxVersion:          [2]int{17, 18},
		EnvelopeKeyOverride: "Cisco-IOS-XE-native:spanning-tree",
		ElementMap: map[string]string{
			"mode":   "Cisco-IOS-XE-spanning-tree:mode",
			"extend": "Cisco-IOS-XE-spanning-tree:extend",
		},
	},

	// ── ip_domain: IOS-XE 26.01 ─────────────────────────────────
	// On 26.01 the domain name leaf moved from domain.name to
	// domain.name-container.name-no-vrf. NetAsCode remains stable as
	// ip_domain.name; the writer translates on the way in and out.
	{
		Family:             "ip_domain",
		MinVersion:         [2]int{26, 1},
		MaxVersion:         [2]int{27, 0},
		BodyTransform:      ipDomainBodyTransform2601,
		FetchBodyTransform: ipDomainFetchTransform2601,
	},

	// ── interface_switchport: IOS-XE < 17.18 ─────────────────────
	// On < 17.18 the switchport sub-container elements need the
	// "Cisco-IOS-XE-switch:" module prefix, and the access VLAN
	// shape is double-nested: access.vlan.vlan (not access.vlan).
	{
		Family:     "interface_switchport",
		MinVersion: [2]int{17, 0},
		MaxVersion: [2]int{17, 18},
		ElementMap: map[string]string{
			"mode":   "Cisco-IOS-XE-switch:mode",
			"access": "Cisco-IOS-XE-switch:access",
			"trunk":  "Cisco-IOS-XE-switch:trunk",
		},
		BodyTransform: switchportBodyTransform1716,
	},

	// ── event_manager: IOS-XE < 17.18 ────────────────────────────
	// On 17.16 the EEM YANG model differs structurally from 17.18:
	//   - event sub-containers use *-choice suffix (timer-choice,
	//     syslog-choice, track-choice, none-choice) and need the
	//     Cisco-IOS-XE-eem: module prefix.
	//   - actions live under action-config.action[] (not action[])
	//     and the action-type containers are renamed (cli→cli-choice,
	//     syslog→syslog-option).
	// BodyTransform handles all renames + prefix additions in one
	// pass (ElementMap is empty because it runs BEFORE BodyTransform
	// and would rename the 17.18 keys before the transform can act).
	// FetchBodyTransform reverses the structural changes on Fetch.
	{
		Family:             "event_manager",
		MinVersion:         [2]int{17, 0},
		MaxVersion:         [2]int{17, 18},
		BodyTransform:      eemBodyTransform1716,
		FetchBodyTransform: eemFetchTransform1716,
	},

	// ── crypto_ipsec_transform_set: already handled ──────────────
	// The transformSetToYANG function in crypto.go already encodes
	// tunnel/transport as empty leaves. No override needed.

	// ── dhcp: IOS-XE < 17.18 ────────────────────────────────────
	// On 17.15/17.16 the DHCP pool YANG model uses "id" as the list
	// key (not "name"), nests network under primary-network, wraps
	// default-router in a list, and requires the Cisco-IOS-XE-dhcp:
	// module prefix on the envelope key. Parent container /ip/dhcp
	// must be created before the pool PUT.
	{
		Family:              "dhcp",
		MinVersion:          [2]int{17, 0},
		MaxVersion:          [2]int{17, 18},
		EnvelopeKeyOverride: "Cisco-IOS-XE-dhcp:pool",
		KeyFieldOverride:    "id",
		NeedParentCreation:  true,
		ParentPath:          "/Cisco-IOS-XE-native:native/ip/dhcp",
		ParentBody:          []byte(`{"Cisco-IOS-XE-native:dhcp":{}}`),
		BodyTransform:       dhcpBodyTransformPre1718,
		FetchBodyTransform:  dhcpFetchTransformPre1718,
	},

	// ── ospf: no version override needed ────────────────────────
	// The ospf writer uses KeyField: "area-id" for area entries
	// (matching the YANG model on all versions). The netascode
	// fixture uses "area-id" as the canonical key. No override.
}
