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

// TACACS+ server Phase-3 writer.
//
// netascode:
//   tacacs_server:
//     servers:
//       - name: primary
//         address:
//           ipv4: 10.0.0.1
//         key: "secret"
//         single_connection: true
//
// YANG: /Cisco-IOS-XE-native:native/tacacs/server. Key: name.

func init() {
	Override(keyedListWriter{
		family:      "tacacs_server",
		yangPath:    "/Cisco-IOS-XE-native:native/tacacs/server",
		envelopeKey: "Cisco-IOS-XE-aaa:server",
		innerKey:    "servers",
		keyField:    "name",
		managedLeaves: []string{
			"address",
			"key",
			"port",
			"single-connection",
			"timeout",
		},
	})
}
