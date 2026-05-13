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

// NTP Phase-2 writer.
//
// netascode shape (common subset):
//
//   ntp:
//     servers:
//       - name: 192.0.2.1
//         prefer: true
//     authentication: true
//     source_interface:
//       Loopback: "0"
//
// YANG: /Cisco-IOS-XE-native:native/ntp/server/server-list
// Key: ip-address (the YANG list key).
//
// Phase-2 manages only the server list here — the broader NTP
// container (authentication, source-interface, master) is a
// follow-up. Treating servers as the identity of the family keeps
// the common case simple for operators.
//
// The netascode input uses "name" to hold the server IP/hostname,
// but the YANG list key is "ip-address". The yangBodyShape and
// yangFetchShape transforms bridge this mismatch.
// Caught against C8000V 17.16.01a: RESTCONF rejected
// {"name":"10.1.1.1"} with missing-element: ip-address.

func init() {
	Override(keyedListWriter{
		family:      "ntp",
		yangPath:    "/Cisco-IOS-XE-native:native/ntp/server/server-list",
		envelopeKey: "Cisco-IOS-XE-ntp:server-list",
		innerKey:    "servers",
		keyField:    "name",
		managedLeaves: []string{
			"prefer",
			"source",
			"key",
			"version",
		},
		yangBodyShape: ntpServerToYANG,
		yangFetchShape: ntpServerFromYANG,
	})
}

// ntpServerToYANG renames the netascode "name" key to the YANG
// list key "ip-address" for the outbound RESTCONF payload.
func ntpServerToYANG(flat map[string]any) map[string]any {
	out := make(map[string]any, len(flat))
	for k, v := range flat {
		if k == "name" {
			out["ip-address"] = v
		} else {
			out[k] = v
		}
	}
	return out
}

// ntpServerFromYANG inverts the rename so observed state matches
// the netascode shape for leavesEqual comparison.
func ntpServerFromYANG(yang map[string]any) map[string]any {
	out := make(map[string]any, len(yang))
	for k, v := range yang {
		if k == "ip-address" {
			out["name"] = v
		} else {
			out[k] = v
		}
	}
	return out
}
