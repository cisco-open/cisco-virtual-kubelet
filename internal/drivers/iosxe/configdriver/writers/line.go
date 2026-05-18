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

// Line (console/vty) Phase-2 writer.
//
// netascode (simplified):
//
//   line:
//     vty:
//       - first: 0
//         last: 4
//         transport_input: ssh
//         login_authentication: default
//
// YANG: /Cisco-IOS-XE-native:native/line/vty. The YANG model keys by
// (first, last); Phase-2 uses "first" as the keyField and treats
// last as a managed leaf so a netascode block stays idiomatic.
//
// Console lines (line/console) and aux (line/aux) follow the same
// pattern and will share this writer's shape in Phase-3.

func init() {
	Override(keyedListWriter{
		family:      "line",
		yangPath:    "/Cisco-IOS-XE-native:native/line/vty",
		envelopeKey: "Cisco-IOS-XE-native:vty",
		innerKey:    "vty",
		keyField:    "first",
		managedLeaves: []string{
			"last",
			"transport",
			"exec-timeout",
			"login",
			"password",
		},
		yangBodyShape:  lineToYANG,
		yangFetchShape: lineFromYANG,
	})
}
