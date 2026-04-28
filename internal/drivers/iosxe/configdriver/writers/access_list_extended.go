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

// Extended access-list writer with per-rule diffing. See
// nested_keyed.go for the shared diff machinery; this file is just
// the family-specific binding.

const (
	aclExtFamily = "access_list_extended"
	// The `extended` list lives in the Cisco-IOS-XE-acl YANG module,
	// not Cisco-IOS-XE-native — so the path's last segment carries
	// the qualified prefix. Without it, the netconf transport's
	// path-aware filter builder emits <extended> with no xmlns
	// declaration and the device rejects with
	// `unknown-element <bad-element>extended</bad-element>`. Caught
	// against the live Cat9300 retest of test 08 (2026-04-28).
	aclExtPath        = "/Cisco-IOS-XE-native:native/ip/access-list/Cisco-IOS-XE-acl:extended"
	aclExtEnvelopeKey = "Cisco-IOS-XE-acl:extended"
	aclExtInnerKey    = "extended"
	aclExtKeyField    = "name"
)

func init() {
	Override(nestedKeyedListWriter{
		base: keyedListWriter{
			family:        aclExtFamily,
			yangPath:      aclExtPath,
			envelopeKey:   aclExtEnvelopeKey,
			innerKey:      aclExtInnerKey,
			keyField:      aclExtKeyField,
			managedLeaves: []string{"rules"},
		},
		nestedLeaf:      "rules",
		nestedKeyField:  "sequence",
		nestedYANGInner: "access-list-seq-rule",
		nestedBodyShape: aclRuleToYANG,
	})
}

// aclRuleToYANG translates a netascode-shape ACL rule into the
// IOS-XE-acl YANG body the device expects under <access-list-seq-rule>.
//
// Schema confirmed via NETCONF <get-schema> for module Cisco-IOS-XE-acl
// against C9K-4 / IOS-XE 17.18.2 (2026-04-28). The IPv4 ext-acl rule
// is `grouping ipv4-ext-acl-grouping` → `list access-list-seq-rule`:
//
//	access-list-seq-rule
//	  sequence                    uint64 (list key)
//	  ace-rule (container)        the ACE choice case wraps everything
//	    action                    enum: deny | permit
//	    protocol                  enum: ip | tcp | udp | icmp | ...
//	    (uses ipv4-acl-src-dst-addr-port-grouping)
//	    ipv4-address + mask       source: network + wildcard bits
//	    any (empty)               source: any
//	    host-address              source: single host
//	    dest-ipv4-address + dest-mask  destination: network + wildcard
//	    dst-any (empty)           destination: any
//	    dst-host-address          destination: single host
//	    src-eq | src-gt | src-lt | src-neq   source port matchers
//	    dst-eq | dst-gt | dst-lt | dst-neq   destination port matchers
//	    log (empty)               log on match
//	  remarks                    leaf-list of comment strings
//	                             (alternate to the ace-rule case)
//
// netascode operator-friendly shorthand (input):
//
//	sequence: 10
//	action: deny | permit
//	protocol: ip | tcp | udp | icmp | ...
//	src_host: 10.0.0.1
//	src_any: true
//	src_prefix: 10.0.0.0
//	src_wildcard: 0.0.0.255
//	dst_host: 10.1.1.1
//	dst_any: true
//	dst_prefix: 10.1.0.0
//	dst_wildcard: 0.0.0.255
//	src_eq: 22
//	dst_eq: 80
//	log: true
//	remark: "comment"
//
// Empty-leaf YANG types (`type empty;`) are encoded in JSON RFC 7951
// as `[null]` and rendered by the netconf json→xml converter as
// self-closing XML elements (e.g. <any/>). Boolean true on netascode
// shorthand maps to `[null]`; any other value is dropped.
//
// `sequence` stays at the access-list-seq-rule level because it is
// the YANG list key; everything else lives under the <ace-rule>
// wrapper. The orderedMapKeys helper in the netconf transport ensures
// <sequence> emits first per YANG strict-order.
//
// Caught against the live C9K-4 retest of test 08 (2026-04-28) — nine
// iterations against the device pinned down the exact wrapper +
// element names, captured via NETCONF <get-schema>.
func aclRuleToYANG(flat map[string]any) map[string]any {
	emptyLeaf := []any{nil}
	out := map[string]any{}
	ace := map[string]any{}

	stash := func(k string, v any) { ace[k] = v }
	stashEmpty := func(k string, v any) {
		if isTrue(v) {
			ace[k] = emptyLeaf
		}
	}

	for k, v := range flat {
		switch k {
		case "sequence":
			out["sequence"] = v
		case "action":
			stash("action", v)
		case "protocol":
			stash("protocol", v)
		case "src_host", "source_host":
			stash("host-address", v)
		case "src_any", "source_any":
			stashEmpty("any", v)
		case "src_prefix", "source_prefix":
			stash("ipv4-address", v)
		case "src_wildcard", "source_wildcard":
			stash("mask", v)
		case "dst_host":
			stash("dst-host-address", v)
		case "dst_any":
			stashEmpty("dst-any", v)
		case "dst_prefix":
			stash("dest-ipv4-address", v)
		case "dst_wildcard":
			stash("dest-mask", v)
		case "src_eq":
			stash("src-eq", v)
		case "src_gt":
			stash("src-gt", v)
		case "src_lt":
			stash("src-lt", v)
		case "src_neq":
			stash("src-neq", v)
		case "dst_eq":
			stash("dst-eq", v)
		case "dst_gt":
			stash("dst-gt", v)
		case "dst_lt":
			stash("dst-lt", v)
		case "dst_neq":
			stash("dst-neq", v)
		case "log":
			stashEmpty("log", v)
		case "remark":
			// remark is the alternate choice case; emit at the
			// access-list-seq-rule level under <remarks> (leaf-list).
			out["remarks"] = []any{v}
		default:
			ace[k] = v
		}
	}

	if len(ace) > 0 {
		out["ace-rule"] = ace
	}
	return out
}
