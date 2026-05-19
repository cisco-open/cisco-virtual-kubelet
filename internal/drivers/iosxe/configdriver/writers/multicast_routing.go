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

// IPv4 multicast-routing enablement writer.
//
// netascode shape:
//
//	multicast_routing:
//	  distributed: true
//
// YANG: /Cisco-IOS-XE-native:native/ip/Cisco-IOS-XE-multicast:multicast-routing
//
// This is a prerequisite for the pim family. Enabling this container
// causes "ip multicast-routing distributed" (router) or
// "ip multicast-routing" (switch) to appear in running-config.

func init() {
	Override(singletonWriter{
		family:        "multicast_routing",
		yangPath:      "/Cisco-IOS-XE-native:native/ip/Cisco-IOS-XE-multicast:multicast-routing",
		envelopeKey:   "Cisco-IOS-XE-multicast:multicast-routing",
		managedLeaves: []string{"distributed"},
	})
}
