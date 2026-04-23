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

// IS-IS Phase-3 writer.

func init() {
	Override(keyedListWriter{
		family:      "isis",
		yangPath:    "/Cisco-IOS-XE-native:native/router/Cisco-IOS-XE-isis:router-isis",
		envelopeKey: "Cisco-IOS-XE-isis:router-isis",
		innerKey:    "processes",
		keyField:    "tag",
		managedLeaves: []string{
			"net",
			"is-type",
			"metric-style",
			"log-adjacency-changes",
			"passive-interface",
			"address-family",
		},
	})
}
