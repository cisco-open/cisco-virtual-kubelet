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

// IP name-server Phase-3 writer.
//
// netascode:
//   ip_name_server:
//     servers: [8.8.8.8, 1.1.1.1]
//
// YANG: /Cisco-IOS-XE-native:native/ip/name-server. Managed as a
// singleton container whose 'no-vrf' leaf is the list of servers.

func init() {
	Override(singletonWriter{
		family:        "ip_name_server",
		yangPath:      "/Cisco-IOS-XE-native:native/ip/name-server",
		envelopeKey:   "Cisco-IOS-XE-native:name-server",
		managedLeaves: []string{"no-vrf", "vrf"},
	})
}
