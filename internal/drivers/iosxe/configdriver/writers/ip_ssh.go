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

// IP SSH server Phase-3 writer.
//
// netascode:
//   ip_ssh:
//     version: 2
//     time_out: 60
//     authentication_retries: 3
//     source_interface:
//       Loopback: "0"
//
// YANG: /Cisco-IOS-XE-native:native/ip/ssh.

func init() {
	Override(singletonWriter{
		family:      "ip_ssh",
		yangPath:    "/Cisco-IOS-XE-native:native/ip/ssh",
		envelopeKey: "Cisco-IOS-XE-native:ssh",
		managedLeaves: []string{
			"version",
			"time-out",
			"authentication-retries",
			"source-interface",
			"bulk-mode",
			"rsa",
			"server",
		},
		yangBodyShape: ipSSHToYANG,
	})
}
