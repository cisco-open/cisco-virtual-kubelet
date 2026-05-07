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

// IP HTTP/HTTPS server Phase-3 writer.
//
// netascode:
//   ip_http:
//     server: true
//     secure_server: true
//     authentication:
//       local: true
//     max_connections: 16
//
// YANG: /Cisco-IOS-XE-native:native/ip/http.

func init() {
	Override(singletonWriter{
		family:      "ip_http",
		yangPath:    "/Cisco-IOS-XE-native:native/ip/http",
		envelopeKey: "Cisco-IOS-XE-http:http",
		managedLeaves: []string{
			"server",
			"secure-server",
			"authentication",
			"max-connections",
			"timeout-policy",
			"client",
		},
	})
}
