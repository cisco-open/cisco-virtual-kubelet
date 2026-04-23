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

// SNMP server Phase-2 writer.
//
// netascode (common subset):
//   snmp_server:
//     community:
//       - name: public
//         access: ro
//     location: "colo-1"
//     contact: "noc@example.com"
//     trap_source_interface:
//       Loopback: "0"
//
// YANG: /Cisco-IOS-XE-native:native/snmp-server. Phase-2 manages the
// commonly-configured leaves; v3 groups/users and engine-id
// management are Phase-3.

func init() {
	Override(singletonWriter{
		family:      "snmp_server",
		yangPath:    "/Cisco-IOS-XE-native:native/snmp-server",
		envelopeKey: "Cisco-IOS-XE-snmp:snmp-server",
		managedLeaves: []string{
			"community",
			"location",
			"contact",
			"trap-source",
			"host",
		},
	})
}
