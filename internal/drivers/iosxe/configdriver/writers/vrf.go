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

// VRF Phase-1 writer.
//
// netascode shape:
//
//   vrf:
//     vrfs:
//       - name: MGMT
//         rd: "65000:1"
//         description: out-of-band
//
// YANG path: /Cisco-IOS-XE-native:native/vrf/definition
// Key: name. Managed leaves: rd, description.

func init() {
	Override(keyedListWriter{
		family:        "vrf",
		yangPath:      "/Cisco-IOS-XE-native:native/vrf/definition",
		envelopeKey:   "Cisco-IOS-XE-native:definition",
		innerKey:      "vrfs",
		keyField:      "name",
		managedLeaves: []string{"rd", "description"},
	})
}
