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

// RADIUS server Phase-3 writer.
//
// netascode:
//   radius_server:
//     servers:
//       - id: primary
//         address:
//           ipv4: 10.0.0.2
//         key: "secret"
//
// YANG: /Cisco-IOS-XE-native:native/radius/server. Key: id.

func init() {
	Override(keyedListWriter{
		family:      "radius_server",
		yangPath:    "/Cisco-IOS-XE-native:native/radius/server",
		envelopeKey: "Cisco-IOS-XE-aaa:server",
		innerKey:    "servers",
		keyField:    "id",
		managedLeaves: []string{
			"address",
			"key",
			"timeout",
			"retransmit",
			"deadtime",
		},
	})
}
