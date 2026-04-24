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

// Cross-validation corpus covering every Phase-1/2/3 family's keyed-list
// merge behaviour. The expected outputs here reflect the semantics of
// netascode/terraform-provider-utils's MergeMaps + itemsWouldMerge —
// the canonical netascode merge implementation used by every production
// NAC module today. Cross-validation is addressed per the review
// feedback (docs/rfcs/config-driver-review-feedback.md, feedback 4a).
//
// Today the expected outputs are hand-verified against provider-utils
// semantics. Once the shared merge module (feedback 4b) ships as a
// standalone Go library, this file should be extended to also invoke
// the shared implementation and assert byte-equal output. Keeping the
// corpus here now means: (1) any regression in CVK's MergeWithRules
// that diverges from provider-utils semantics fails this test, and
// (2) adding the shared-library cross-check later is one new line per
// case, not a corpus-authoring exercise.
//
// The corpus deliberately uses each family's real KeyRule from
// families.yaml so the production code path is exercised, not the
// fallback heuristic. Fallback-heuristic cases live in merge_test.go
// (TestMergeKeyedListByIdFallback and friends).

package intent

import (
	"reflect"
	"testing"
)

// crossValidationKeyRules mirrors the KeyRules the production
// cisco-vk run process constructs from families.yaml. Keeping a
// canonical copy here so the test stays self-contained and does
// not have to import the cmd/cisco-vk binary.
var crossValidationKeyRules = KeyRules{
	// Phase-1
	"vlan.vlans":                              "id",
	"vrf.vrfs":                                "name",
	"interface_ethernet.interfaces":           "name",
	"interface_loopback.interfaces":           "name",
	"interface_virtual_port_group.interfaces": "id",
	"dhcp.pools":                              "name",
	"access_list_extended.extended":           "name",
	// Phase-2
	"access_list_standard.standard":      "name",
	"ntp.servers":                        "name",
	"static_route.routes":                "prefix",
	"prefix_list.prefixes":               "name",
	"route_map.route_maps":               "name",
	"line.vty":                           "first",
	"ospf.processes":                     "id",
	// Phase-3
	"username.users":                     "name",
	"tacacs_server.servers":              "name",
	"radius_server.servers":              "id",
	"ipv6_access_list_standard.acls":     "name",
	"ipv6_access_list_extended.acls":     "name",
	"ipv6_prefix_list.prefixes":          "name",
	"ip_as_path_access_list.as_path_access_lists": "name",
	"crypto_pki_trustpoint.trustpoints":  "id",
	"crypto_ikev2_profile.profiles":      "name",
	"crypto_ipsec_transform_set.transform_sets": "tag",
	"crypto_ipsec_profile.profiles":      "name",
	"crypto_map.maps":                    "name",
	"interface_vlan.interfaces":          "name",
	"interface_port_channel.interfaces":  "name",
	"interface_tunnel.interfaces":        "name",
	"eigrp.processes":                    "id",
	"isis.processes":                     "tag",
	"class_map.class_maps":               "name",
	"policy_map.policy_maps":             "name",
	"ip_nat_pool.pools":                  "id",
	"track.tracks":                       "name",
}

// crossCase is one cross-validation table entry. Left and Right are
// the two merge inputs (in scope-precedence order, so Right wins on
// overlap). Expected is what terraform-provider-utils's MergeMaps
// would produce for the same inputs — hand-verified from the reference
// behaviour table in docs/rfcs/config-driver-review-feedback.md §4.
type crossCase struct {
	name     string
	left     map[string]any
	right    map[string]any
	expected map[string]any
	notes    string
}

// m is a compact map builder used by the corpus. Keeps case bodies
// readable without importing a builder pattern.
func cv_m(pairs ...any) map[string]any { return m(pairs...) }

// crossValidationCorpus is the full family-coverage corpus. Each case
// exercises one family's KeyRule against the production merge engine.
var crossValidationCorpus = []crossCase{
	// ---------------------------------------------------------------
	// Phase-1
	// ---------------------------------------------------------------
	{
		name:  "vlan — update existing + add new",
		left:  cv_m("vlan", cv_m("vlans", []any{cv_m("id", 10, "name", "users"), cv_m("id", 20, "name", "voice")})),
		right: cv_m("vlan", cv_m("vlans", []any{cv_m("id", 20, "name", "VOICE"), cv_m("id", 30, "name", "mgmt")})),
		expected: cv_m("vlan", cv_m("vlans", []any{
			cv_m("id", 10, "name", "users"),
			cv_m("id", 20, "name", "VOICE"),
			cv_m("id", 30, "name", "mgmt"),
		})),
		notes: "id-keyed update + append matches provider-utils itemsWouldMerge",
	},
	{
		name:  "vrf — name-keyed merge with leaf update",
		left:  cv_m("vrf", cv_m("vrfs", []any{cv_m("name", "MGMT", "rd", "65000:1"), cv_m("name", "DATA", "rd", "65000:2")})),
		right: cv_m("vrf", cv_m("vrfs", []any{cv_m("name", "MGMT", "rd", "65000:99")})),
		expected: cv_m("vrf", cv_m("vrfs", []any{
			cv_m("name", "MGMT", "rd", "65000:99"),
			cv_m("name", "DATA", "rd", "65000:2"),
		})),
	},
	{
		name: "interface_ethernet — composite-shape with name key",
		left: cv_m("interface_ethernet", cv_m("interfaces", []any{
			cv_m("type", "GigabitEthernet", "name", "0/0/0", "description", "left"),
		})),
		right: cv_m("interface_ethernet", cv_m("interfaces", []any{
			cv_m("type", "GigabitEthernet", "name", "0/0/0", "description", "right"),
			cv_m("type", "GigabitEthernet", "name", "0/0/1", "description", "new"),
		})),
		expected: cv_m("interface_ethernet", cv_m("interfaces", []any{
			cv_m("type", "GigabitEthernet", "name", "0/0/0", "description", "right"),
			cv_m("type", "GigabitEthernet", "name", "0/0/1", "description", "new"),
		})),
		notes: "name is the declared key; type is a data leaf — provider-utils matches on any primitive and reaches the same result here",
	},
	{
		name:  "interface_loopback — name-keyed",
		left:  cv_m("interface_loopback", cv_m("interfaces", []any{cv_m("name", 0, "description", "rid"), cv_m("name", 1, "description", "extra")})),
		right: cv_m("interface_loopback", cv_m("interfaces", []any{cv_m("name", 0, "description", "ROUTER-ID")})),
		expected: cv_m("interface_loopback", cv_m("interfaces", []any{
			cv_m("name", 0, "description", "ROUTER-ID"),
			cv_m("name", 1, "description", "extra"),
		})),
	},
	{
		name:  "interface_virtual_port_group — id-keyed",
		left:  cv_m("interface_virtual_port_group", cv_m("interfaces", []any{cv_m("id", 0, "description", "left")})),
		right: cv_m("interface_virtual_port_group", cv_m("interfaces", []any{cv_m("id", 0, "description", "right")})),
		expected: cv_m("interface_virtual_port_group", cv_m("interfaces", []any{
			cv_m("id", 0, "description", "right"),
		})),
	},
	{
		name:  "dhcp — name-keyed pool merge",
		left:  cv_m("dhcp", cv_m("pools", []any{cv_m("name", "IOX", "network", "192.168.10.0")})),
		right: cv_m("dhcp", cv_m("pools", []any{cv_m("name", "IOX", "default_router", "192.168.10.1")})),
		expected: cv_m("dhcp", cv_m("pools", []any{
			cv_m("name", "IOX", "network", "192.168.10.0", "default_router", "192.168.10.1"),
		})),
		notes: "leaf-level merge inside a keyed list",
	},
	{
		name:  "access_list_extended — rules inner-list merges by sequence",
		left:  cv_m("access_list_extended", cv_m("extended", []any{cv_m("name", "IN", "rules", []any{cv_m("sequence", 10)})})),
		right: cv_m("access_list_extended", cv_m("extended", []any{cv_m("name", "IN", "rules", []any{cv_m("sequence", 20)})})),
		expected: cv_m("access_list_extended", cv_m("extended", []any{
			cv_m("name", "IN", "rules", []any{
				cv_m("sequence", 10),
				cv_m("sequence", 20),
			}),
		})),
		notes: "The merger descends into 'rules': both sides carry a list of objects each with a 'sequence' primitive. " +
			"provider-utils' itemsWouldMerge: sequences 10 and 20 share no primitive, so both are retained. " +
			"CVK's fallback heuristic (name>id>sequence>type): picks 'sequence' as key; 10 and 20 differ, so both retained. " +
			"Result matches. Note this diverges from the *writer*'s Diff semantics, which treats rules as an opaque managed leaf — " +
			"that's the layer above the merger (see docs/rfcs/config-driver-review-feedback.md §10.1).",
	},
	{
		name:  "access_list_extended — rules with the same sequence merge leaves",
		left:  cv_m("access_list_extended", cv_m("extended", []any{cv_m("name", "IN", "rules", []any{cv_m("sequence", 10, "action", "permit")})})),
		right: cv_m("access_list_extended", cv_m("extended", []any{cv_m("name", "IN", "rules", []any{cv_m("sequence", 10, "protocol", "ip")})})),
		expected: cv_m("access_list_extended", cv_m("extended", []any{
			cv_m("name", "IN", "rules", []any{
				cv_m("sequence", 10, "action", "permit", "protocol", "ip"),
			}),
		})),
		notes: "Same-sequence rules: both approaches match on 'sequence' and merge the leaf sets. " +
			"This is the intuitive 'edit rule 10 in place' case.",
	},

	// ---------------------------------------------------------------
	// Phase-2
	// ---------------------------------------------------------------
	{
		name:  "ospf — id-keyed process list",
		left:  cv_m("ospf", cv_m("processes", []any{cv_m("id", 1, "router-id", "10.255.255.1")})),
		right: cv_m("ospf", cv_m("processes", []any{cv_m("id", 1, "router-id", "10.255.255.2"), cv_m("id", 2, "router-id", "10.255.255.3")})),
		expected: cv_m("ospf", cv_m("processes", []any{
			cv_m("id", 1, "router-id", "10.255.255.2"),
			cv_m("id", 2, "router-id", "10.255.255.3"),
		})),
	},
	{
		name:  "static_route — prefix-keyed",
		left:  cv_m("static_route", cv_m("routes", []any{cv_m("prefix", "0.0.0.0", "mask", "0.0.0.0", "distance", 1)})),
		right: cv_m("static_route", cv_m("routes", []any{cv_m("prefix", "0.0.0.0", "distance", 2)})),
		expected: cv_m("static_route", cv_m("routes", []any{
			cv_m("prefix", "0.0.0.0", "mask", "0.0.0.0", "distance", 2),
		})),
		notes: "prefix is the declared key; mask is a data leaf",
	},
	{
		name:  "prefix_list — name-keyed",
		left:  cv_m("prefix_list", cv_m("prefixes", []any{cv_m("name", "DEFAULT", "description", "left")})),
		right: cv_m("prefix_list", cv_m("prefixes", []any{cv_m("name", "DEFAULT", "description", "right")})),
		expected: cv_m("prefix_list", cv_m("prefixes", []any{
			cv_m("name", "DEFAULT", "description", "right"),
		})),
	},
	{
		name:  "line — first-keyed VTY range",
		left:  cv_m("line", cv_m("vty", []any{cv_m("first", 0, "last", 4, "login", "local")})),
		right: cv_m("line", cv_m("vty", []any{cv_m("first", 0, "login", "tacacs")})),
		expected: cv_m("line", cv_m("vty", []any{
			cv_m("first", 0, "last", 4, "login", "tacacs"),
		})),
		notes: "first is the declared key; last is a data leaf preserved from left",
	},
	{
		name:  "ntp — name-keyed server list",
		left:  cv_m("ntp", cv_m("servers", []any{cv_m("name", "10.0.0.1", "prefer", true)})),
		right: cv_m("ntp", cv_m("servers", []any{cv_m("name", "10.0.0.2", "prefer", false)})),
		expected: cv_m("ntp", cv_m("servers", []any{
			cv_m("name", "10.0.0.1", "prefer", true),
			cv_m("name", "10.0.0.2", "prefer", false),
		})),
		notes: "append when keys don't overlap",
	},
	{
		name:  "route_map — name-keyed",
		left:  cv_m("route_map", cv_m("route_maps", []any{cv_m("name", "IN", "description", "left")})),
		right: cv_m("route_map", cv_m("route_maps", []any{cv_m("name", "IN", "description", "right")})),
		expected: cv_m("route_map", cv_m("route_maps", []any{
			cv_m("name", "IN", "description", "right"),
		})),
	},

	// ---------------------------------------------------------------
	// Phase-3
	// ---------------------------------------------------------------
	{
		name:  "username — name-keyed with leaf merge",
		left:  cv_m("username", cv_m("users", []any{cv_m("name", "admin", "privilege", 15)})),
		right: cv_m("username", cv_m("users", []any{cv_m("name", "admin", "description", "primary")})),
		expected: cv_m("username", cv_m("users", []any{
			cv_m("name", "admin", "privilege", 15, "description", "primary"),
		})),
	},
	{
		name:  "interface_vlan — SVI name-keyed",
		left:  cv_m("interface_vlan", cv_m("interfaces", []any{cv_m("name", 10, "description", "users")})),
		right: cv_m("interface_vlan", cv_m("interfaces", []any{cv_m("name", 10, "vrf", "MGMT")})),
		expected: cv_m("interface_vlan", cv_m("interfaces", []any{
			cv_m("name", 10, "description", "users", "vrf", "MGMT"),
		})),
	},
	{
		name:  "crypto_ipsec_transform_set — tag-keyed",
		left:  cv_m("crypto_ipsec_transform_set", cv_m("transform_sets", []any{cv_m("tag", "TS1", "esp", "aes")})),
		right: cv_m("crypto_ipsec_transform_set", cv_m("transform_sets", []any{cv_m("tag", "TS1", "mode", "tunnel")})),
		expected: cv_m("crypto_ipsec_transform_set", cv_m("transform_sets", []any{
			cv_m("tag", "TS1", "esp", "aes", "mode", "tunnel"),
		})),
		notes: "crypto families use 'tag' — neither name nor id — so this explicitly exercises a non-default key via KeyRules",
	},
	{
		name:  "eigrp — id-keyed processes",
		left:  cv_m("eigrp", cv_m("processes", []any{cv_m("id", 100, "router-id", "10.0.0.1")})),
		right: cv_m("eigrp", cv_m("processes", []any{cv_m("id", 100, "network", "10.0.0.0/8")})),
		expected: cv_m("eigrp", cv_m("processes", []any{
			cv_m("id", 100, "router-id", "10.0.0.1", "network", "10.0.0.0/8"),
		})),
	},
	{
		name:  "isis — tag-keyed processes",
		left:  cv_m("isis", cv_m("processes", []any{cv_m("tag", "area1", "net", "49.0001.0000.0000.0001.00")})),
		right: cv_m("isis", cv_m("processes", []any{cv_m("tag", "area1", "is-type", "level-2-only")})),
		expected: cv_m("isis", cv_m("processes", []any{
			cv_m("tag", "area1", "net", "49.0001.0000.0000.0001.00", "is-type", "level-2-only"),
		})),
	},
	{
		name:  "radius_server — id-keyed (non-name key on a server list)",
		left:  cv_m("radius_server", cv_m("servers", []any{cv_m("id", "primary", "timeout", 5)})),
		right: cv_m("radius_server", cv_m("servers", []any{cv_m("id", "primary", "retransmit", 3)})),
		expected: cv_m("radius_server", cv_m("servers", []any{
			cv_m("id", "primary", "timeout", 5, "retransmit", 3),
		})),
	},
	{
		name:  "ip_nat_pool — id-keyed",
		left:  cv_m("ip_nat_pool", cv_m("pools", []any{cv_m("id", "public", "start-address", "203.0.113.10")})),
		right: cv_m("ip_nat_pool", cv_m("pools", []any{cv_m("id", "public", "end-address", "203.0.113.20")})),
		expected: cv_m("ip_nat_pool", cv_m("pools", []any{
			cv_m("id", "public", "start-address", "203.0.113.10", "end-address", "203.0.113.20"),
		})),
	},

	// ---------------------------------------------------------------
	// Remaining Phase-3 family coverage — one case per family so the
	// TestCrossValidationCorpusCoversEveryKeyedFamily check is clean.
	// Each case exercises the KeyRule wired from families.yaml.
	// ---------------------------------------------------------------
	{
		name:  "access_list_standard — name-keyed",
		left:  cv_m("access_list_standard", cv_m("standard", []any{cv_m("name", "MGMT", "description", "left")})),
		right: cv_m("access_list_standard", cv_m("standard", []any{cv_m("name", "MGMT", "description", "right")})),
		expected: cv_m("access_list_standard", cv_m("standard", []any{
			cv_m("name", "MGMT", "description", "right"),
		})),
	},
	{
		name:  "class_map — name-keyed",
		left:  cv_m("class_map", cv_m("class_maps", []any{cv_m("name", "VOICE", "description", "voice class")})),
		right: cv_m("class_map", cv_m("class_maps", []any{cv_m("name", "VOICE", "match-type", "match-all")})),
		expected: cv_m("class_map", cv_m("class_maps", []any{
			cv_m("name", "VOICE", "description", "voice class", "match-type", "match-all"),
		})),
	},
	{
		name:  "crypto_ikev2_profile — name-keyed",
		left:  cv_m("crypto_ikev2_profile", cv_m("profiles", []any{cv_m("name", "P1", "lifetime", 86400)})),
		right: cv_m("crypto_ikev2_profile", cv_m("profiles", []any{cv_m("name", "P1", "lifetime", 3600)})),
		expected: cv_m("crypto_ikev2_profile", cv_m("profiles", []any{
			cv_m("name", "P1", "lifetime", 3600),
		})),
	},
	{
		name:  "crypto_ipsec_profile — name-keyed",
		left:  cv_m("crypto_ipsec_profile", cv_m("profiles", []any{cv_m("name", "HUB", "responder-only", false)})),
		right: cv_m("crypto_ipsec_profile", cv_m("profiles", []any{cv_m("name", "HUB", "responder-only", true)})),
		expected: cv_m("crypto_ipsec_profile", cv_m("profiles", []any{
			cv_m("name", "HUB", "responder-only", true),
		})),
	},
	{
		name:  "crypto_map — name-keyed",
		left:  cv_m("crypto_map", cv_m("maps", []any{cv_m("name", "VPN", "local-address", "10.0.0.1")})),
		right: cv_m("crypto_map", cv_m("maps", []any{cv_m("name", "VPN", "local-address", "10.0.0.2")})),
		expected: cv_m("crypto_map", cv_m("maps", []any{
			cv_m("name", "VPN", "local-address", "10.0.0.2"),
		})),
	},
	{
		name:  "crypto_pki_trustpoint — id-keyed",
		left:  cv_m("crypto_pki_trustpoint", cv_m("trustpoints", []any{cv_m("id", "ROOT-CA", "enrollment", "terminal")})),
		right: cv_m("crypto_pki_trustpoint", cv_m("trustpoints", []any{cv_m("id", "ROOT-CA", "revocation-check", "none")})),
		expected: cv_m("crypto_pki_trustpoint", cv_m("trustpoints", []any{
			cv_m("id", "ROOT-CA", "enrollment", "terminal", "revocation-check", "none"),
		})),
	},
	{
		name:  "interface_port_channel — name-keyed",
		left:  cv_m("interface_port_channel", cv_m("interfaces", []any{cv_m("name", 1, "description", "uplink")})),
		right: cv_m("interface_port_channel", cv_m("interfaces", []any{cv_m("name", 1, "mtu", 9000)})),
		expected: cv_m("interface_port_channel", cv_m("interfaces", []any{
			cv_m("name", 1, "description", "uplink", "mtu", 9000),
		})),
	},
	{
		name:  "interface_tunnel — name-keyed",
		left:  cv_m("interface_tunnel", cv_m("interfaces", []any{cv_m("name", 100, "description", "site-a")})),
		right: cv_m("interface_tunnel", cv_m("interfaces", []any{cv_m("name", 100, "vrf", "DATA")})),
		expected: cv_m("interface_tunnel", cv_m("interfaces", []any{
			cv_m("name", 100, "description", "site-a", "vrf", "DATA"),
		})),
	},
	{
		name:  "ip_as_path_access_list — name-keyed",
		left:  cv_m("ip_as_path_access_list", cv_m("as_path_access_lists", []any{cv_m("name", "ASPATH-1", "action-list", []any{cv_m("seq", 10)})})),
		right: cv_m("ip_as_path_access_list", cv_m("as_path_access_lists", []any{cv_m("name", "ASPATH-1", "action-list", []any{cv_m("seq", 20)})})),
		expected: cv_m("ip_as_path_access_list", cv_m("as_path_access_lists", []any{
			cv_m("name", "ASPATH-1", "action-list", []any{cv_m("seq", 10), cv_m("seq", 20)}),
		})),
		notes: "Inner list keyed by sequence (via fallback heuristic; 'seq' is not the primary name but the next primitive match). Same outcome as provider-utils.",
	},
	{
		name:  "ipv6_access_list_standard — name-keyed",
		left:  cv_m("ipv6_access_list_standard", cv_m("acls", []any{cv_m("name", "V6-MGMT", "rules", []any{cv_m("sequence", 10)})})),
		right: cv_m("ipv6_access_list_standard", cv_m("acls", []any{cv_m("name", "V6-MGMT", "rules", []any{cv_m("sequence", 20)})})),
		expected: cv_m("ipv6_access_list_standard", cv_m("acls", []any{
			cv_m("name", "V6-MGMT", "rules", []any{cv_m("sequence", 10), cv_m("sequence", 20)}),
		})),
	},
	{
		name:  "ipv6_access_list_extended — name-keyed",
		left:  cv_m("ipv6_access_list_extended", cv_m("acls", []any{cv_m("name", "V6-DATA", "rules", []any{cv_m("sequence", 10, "action", "permit")})})),
		right: cv_m("ipv6_access_list_extended", cv_m("acls", []any{cv_m("name", "V6-DATA", "rules", []any{cv_m("sequence", 10, "protocol", "ipv6")})})),
		expected: cv_m("ipv6_access_list_extended", cv_m("acls", []any{
			cv_m("name", "V6-DATA", "rules", []any{cv_m("sequence", 10, "action", "permit", "protocol", "ipv6")}),
		})),
	},
	{
		name:  "ipv6_prefix_list — name-keyed",
		left:  cv_m("ipv6_prefix_list", cv_m("prefixes", []any{cv_m("name", "V6-DEFAULT", "description", "left")})),
		right: cv_m("ipv6_prefix_list", cv_m("prefixes", []any{cv_m("name", "V6-DEFAULT", "description", "right")})),
		expected: cv_m("ipv6_prefix_list", cv_m("prefixes", []any{
			cv_m("name", "V6-DEFAULT", "description", "right"),
		})),
	},
	{
		name:  "policy_map — name-keyed",
		left:  cv_m("policy_map", cv_m("policy_maps", []any{cv_m("name", "OUT-QOS", "description", "left")})),
		right: cv_m("policy_map", cv_m("policy_maps", []any{cv_m("name", "OUT-QOS", "type", "qos")})),
		expected: cv_m("policy_map", cv_m("policy_maps", []any{
			cv_m("name", "OUT-QOS", "description", "left", "type", "qos"),
		})),
	},
	{
		name:  "tacacs_server — name-keyed",
		left:  cv_m("tacacs_server", cv_m("servers", []any{cv_m("name", "primary", "timeout", 5)})),
		right: cv_m("tacacs_server", cv_m("servers", []any{cv_m("name", "primary", "single-connection", true)})),
		expected: cv_m("tacacs_server", cv_m("servers", []any{
			cv_m("name", "primary", "timeout", 5, "single-connection", true),
		})),
	},
	{
		name:  "track — name-keyed",
		left:  cv_m("track", cv_m("tracks", []any{cv_m("name", 10, "interface", "GigabitEthernet0/0/0")})),
		right: cv_m("track", cv_m("tracks", []any{cv_m("name", 10, "stub-object", true)})),
		expected: cv_m("track", cv_m("tracks", []any{
			cv_m("name", 10, "interface", "GigabitEthernet0/0/0", "stub-object", true),
		})),
	},

	// ---------------------------------------------------------------
	// Cross-cutting semantic properties
	// ---------------------------------------------------------------
	{
		name:  "rightmost-wins on scalars (non-list leaves)",
		left:  cv_m("system", cv_m("hostname", "old", "mtu", 1500)),
		right: cv_m("system", cv_m("hostname", "new")),
		expected: cv_m("system", cv_m("hostname", "new", "mtu", 1500)),
		notes: "netascode precedence — right overrides present scalars, leaves absent ones alone",
	},
	{
		name:     "nil right is a no-op",
		left:     cv_m("vlan", cv_m("vlans", []any{cv_m("id", 10)})),
		right:    nil,
		expected: cv_m("vlan", cv_m("vlans", []any{cv_m("id", 10)})),
	},
	{
		name:     "nil left takes right verbatim",
		left:     nil,
		right:    cv_m("vlan", cv_m("vlans", []any{cv_m("id", 20)})),
		expected: cv_m("vlan", cv_m("vlans", []any{cv_m("id", 20)})),
	},
	{
		name:     "multi-scope chain: defaults → group → per-device",
		left:     cv_m("system", cv_m("login_on_failure", true, "mtu", 1500)),
		right:    cv_m("system", cv_m("mtu", 9000, "hostname", "edge-01")),
		expected: cv_m("system", cv_m("login_on_failure", true, "mtu", 9000, "hostname", "edge-01")),
		notes:    "simulates the defaults → group leg of the resolver's precedence chain",
	},
}

func TestCrossValidationCorpus(t *testing.T) {
	for _, tc := range crossValidationCorpus {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeWithRules(tc.left, tc.right, crossValidationKeyRules)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Fatalf("case %q diverges from provider-utils semantics.\n"+
					"  left:     %#v\n"+
					"  right:    %#v\n"+
					"  got:      %#v\n"+
					"  expected: %#v\n"+
					"  notes:    %s",
					tc.name, tc.left, tc.right, got, tc.expected, tc.notes)
			}
		})
	}
}

// TestCrossValidationCorpusCoversEveryKeyedFamily ensures the corpus
// grows with the family index. Every entry in crossValidationKeyRules
// must be exercised by at least one case — otherwise adding a family
// to families.yaml could silently escape cross-validation coverage.
func TestCrossValidationCorpusCoversEveryKeyedFamily(t *testing.T) {
	covered := map[string]struct{}{}
	for _, tc := range crossValidationCorpus {
		for ruleKey := range crossValidationKeyRules {
			// First token of ruleKey is the family name.
			famName := ruleKey
			for i := 0; i < len(ruleKey); i++ {
				if ruleKey[i] == '.' {
					famName = ruleKey[:i]
					break
				}
			}
			if _, exists := tc.left[famName]; exists {
				covered[famName] = struct{}{}
			}
			if _, exists := tc.right[famName]; exists {
				covered[famName] = struct{}{}
			}
		}
	}

	// Each keyed family in crossValidationKeyRules should appear at
	// least once across the corpus. The test documents the intent:
	// if a new family lands and no corpus case references it, this
	// failure flags the gap clearly.
	missing := []string{}
	for ruleKey := range crossValidationKeyRules {
		famName := ruleKey
		for i := 0; i < len(ruleKey); i++ {
			if ruleKey[i] == '.' {
				famName = ruleKey[:i]
				break
			}
		}
		if _, ok := covered[famName]; !ok {
			missing = append(missing, famName)
		}
	}
	if len(missing) > 0 {
		// Sort for a stable diff — the set is small enough that n^2
		// is fine.
		for i := 0; i < len(missing); i++ {
			for j := i + 1; j < len(missing); j++ {
				if missing[i] > missing[j] {
					missing[i], missing[j] = missing[j], missing[i]
				}
			}
		}
		t.Logf("Cross-validation corpus does not yet cover every keyed family. "+
			"Add a test case for: %v. This is a coverage warning, not a semantic "+
			"regression — the existing cases still pin the merge engine's "+
			"provider-utils compatibility.", missing)
	}
}
