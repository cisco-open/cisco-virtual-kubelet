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

// Spanning-tree global Phase-3 writer.

func init() {
	Override(singletonWriter{
		family:      "spanning_tree",
		yangPath:    "/Cisco-IOS-XE-native:native/spanning-tree",
		envelopeKey: "Cisco-IOS-XE-spanning-tree:spanning-tree",
		managedLeaves: []string{
			"mode",
			"extend",
			"portfast",
			"vlan",
			"mst",
		},
		// Note: `mode` leaf causes malformed-message on C9KV 17.15.
		// A runtime yangBodyShape could filter it on < 17.18, but
		// singletonWriter doesn't support yangBodyShape. A custom
		// writer or a per-version leaf filter is needed if mode must
		// be excluded on < 17.18 platforms.
	})
}
