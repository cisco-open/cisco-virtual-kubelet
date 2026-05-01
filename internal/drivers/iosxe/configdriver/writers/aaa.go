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

// AAA Phase-2 writer.
//
// netascode (common subset):
//   aaa:
//     new_model: true
//     authentication:
//       login:
//         - name: default
//           method: local
//     authorization:
//       exec:
//         - name: default
//           method: local
//
// YANG: /Cisco-IOS-XE-native:native/aaa. AAA is deeply nested and
// site-specific. Phase-2 exposes the top-level knobs that are
// universally present; Phase-3 will add per-method-list diffing once
// real operator repositories inform the ranking.

func init() {
	Override(singletonWriter{
		family:      "aaa",
		yangPath:    "/Cisco-IOS-XE-native:native/aaa",
		envelopeKey: "Cisco-IOS-XE-aaa:aaa",
		managedLeaves: []string{
			"new-model",
			"authentication",
			"authorization",
			"accounting",
			"session-id",
			"group",
		},
	})
}
