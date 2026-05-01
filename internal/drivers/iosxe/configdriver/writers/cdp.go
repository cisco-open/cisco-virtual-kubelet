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

// CDP Phase-2 writer.
//
// netascode:
//   cdp:
//     run: true
//     advertise_v2: true
//
// YANG: /Cisco-IOS-XE-native:native/cdp. Managed leaves: run,
// advertise_v2. Anything outside the set is left untouched on the
// device per the additive-merge semantics.

func init() {
	Override(singletonWriter{
		family:        "cdp",
		yangPath:      "/Cisco-IOS-XE-native:native/cdp",
		envelopeKey:   "Cisco-IOS-XE-native:cdp",
		managedLeaves: []string{"run", "advertise_v2"},
	})
}
