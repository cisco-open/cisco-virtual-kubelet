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

// aclRuleToYANG translates a netascode-shape ACL rule into the flat
// IOS-XE-acl YANG body shape the device expects under
// <access-list-seq-rule>.
//
// netascode-flat (operator-friendly shorthand):
//
//	sequence: 10
//	action: deny | permit
//	protocol: ip | tcp | udp | icmp | ...
//	src_host: 10.0.0.1            # source: single host
//	src_any: true                 # source: any (empty leaf)
//	src_prefix: 10.0.0.0          # source: network address
//	src_wildcard: 0.0.0.255       # source: wildcard mask
//	dst_host: 10.1.1.1            # destination: single host
//	dst_any: true                 # destination: any (empty leaf)
//	dst_prefix: 10.1.0.0
//	dst_wildcard: 0.0.0.255
//	src_eq: 22                    # source port eq
//	dst_eq: 80                    # destination port eq
//	log: true                     # log on match (empty leaf)
//	remark: "comment"             # rule remark (replaces the ACE)
//
// IOS-XE-acl YANG (flat, all siblings of <sequence>):
//
//	<sequence>            # uint32
//	<ace-rule-action>     # enum: permit | deny
//	<protocol>            # enum/uint
//	<host>                # source-host
//	<any/>                # source-any (empty)
//	<ipv4-address>        # source network
//	<mask>                # source wildcard
//	<dest-host>           # destination-host
//	<dst-any/>            # destination-any (empty)
//	<dest-ipv4-address>   # destination network
//	<dest-mask>           # destination wildcard
//	<src-eq>              # source-port eq
//	<dst-eq>              # destination-port eq
//	<log/>                # empty
//	<remark>              # remark text
//
// Empty-leaf YANG types (`type empty;`) are encoded in JSON RFC 7951 as
// `[null]`; the netconf transport's json→xml converter renders these
// as self-closing elements (e.g. <any/>). Boolean true on netascode
// shorthand maps to `[null]`; any other value is dropped (operator
// must explicitly opt in).
//
// Caught against the live Cat9300 retest of test 08 (2026-04-28)
// where the apply hit `unknown-element <bad-element>src_host</bad-element>`
// because the writer passed the netascode shorthand through verbatim.
func aclRuleToYANG(flat map[string]any) map[string]any {
	out := make(map[string]any, len(flat))
	emptyLeaf := []any{nil}
	for k, v := range flat {
		switch k {
		case "sequence":
			out["sequence"] = v
		case "action":
			out["ace-rule-action"] = v
		case "protocol":
			out["protocol"] = v
		case "src_host", "source_host":
			out["host"] = v
		case "src_any", "source_any":
			if isTrue(v) {
				out["any"] = emptyLeaf
			}
		case "src_prefix", "source_prefix":
			out["ipv4-address"] = v
		case "src_wildcard", "source_wildcard":
			out["mask"] = v
		case "dst_host":
			out["dest-host"] = v
		case "dst_any":
			if isTrue(v) {
				out["dst-any"] = emptyLeaf
			}
		case "dst_prefix":
			out["dest-ipv4-address"] = v
		case "dst_wildcard":
			out["dest-mask"] = v
		case "src_eq":
			out["src-eq"] = v
		case "src_gt":
			out["src-gt"] = v
		case "src_lt":
			out["src-lt"] = v
		case "src_neq":
			out["src-neq"] = v
		case "dst_eq":
			out["dst-eq"] = v
		case "dst_gt":
			out["dst-gt"] = v
		case "dst_lt":
			out["dst-lt"] = v
		case "dst_neq":
			out["dst-neq"] = v
		case "log":
			if isTrue(v) {
				out["log"] = emptyLeaf
			}
		case "remark":
			out["remark"] = v
		default:
			out[k] = v
		}
	}
	return out
}
