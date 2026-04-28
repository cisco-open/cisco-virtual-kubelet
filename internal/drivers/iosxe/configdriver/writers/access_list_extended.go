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
	})
}
