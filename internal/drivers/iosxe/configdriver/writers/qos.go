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

// QoS Phase-3 writer set: class-map and policy-map. A class-map
// classifies traffic; a policy-map declares actions on matched
// classes. Phase-3 manages their top-level structure; per-class
// action diffing is Phase-4 if operator demand appears.

func init() {
	Override(keyedListWriter{
		family:      "class_map",
		yangPath:    "/Cisco-IOS-XE-native:native/policy/Cisco-IOS-XE-policy:class-map",
		envelopeKey: "Cisco-IOS-XE-policy:class-map",
		innerKey:    "class_maps",
		keyField:    "name",
		managedLeaves: []string{
			"description",
			"prematch",
			"match",
			"match-type",
		},
	})

	// policy-map: per-class action diffing so an edit to one
	// class's actions doesn't re-push the whole policy-map's
	// class list.
	Override(nestedKeyedListWriter{
		base: keyedListWriter{
			family:      "policy_map",
			yangPath:    "/Cisco-IOS-XE-native:native/policy/Cisco-IOS-XE-policy:policy-map",
			envelopeKey: "Cisco-IOS-XE-policy:policy-map",
			innerKey:    "policy_maps",
			keyField:    "name",
			managedLeaves: []string{
				"description",
				"class",
				"type",
			},
		},
		nested: []nestedListSpec{
			{Leaf: "class", KeyField: "name"},
		},
	})
}
