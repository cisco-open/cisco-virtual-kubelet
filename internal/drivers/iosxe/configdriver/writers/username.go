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

// Local user Phase-3 writer.
//
// netascode:
//   username:
//     users:
//       - name: admin
//         privilege: 15
//         secret:
//           type: 9
//           secret: "$9$..."   # operator-side encrypted; never logged
//
// YANG: /Cisco-IOS-XE-native:native/username. Key: name.
// The password/secret sub-leaves carry credentials — the writer's
// additive-merge semantics mean a device-side password not in the
// intent is not erased; Phase-3 does not attempt to round-trip
// secrets.

func init() {
	Override(keyedListWriter{
		family:      "username",
		yangPath:    "/Cisco-IOS-XE-native:native/username",
		envelopeKey: "Cisco-IOS-XE-native:username",
		innerKey:    "users",
		keyField:    "name",
		managedLeaves: []string{
			"privilege",
			"secret",
			"password",
			"description",
			"view",
		},
		yangBodyShape:  usernameToYANG,
		yangFetchShape: usernameFromYANG,
	})
}
