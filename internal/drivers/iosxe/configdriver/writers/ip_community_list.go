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

// BGP community-list Phase-3 writer.
//
// Cisco-IOS-XE-native splits community-lists into
// standard/expanded sub-containers under ip/community-list; Phase-3
// manages the whole container as a singleton because the two
// sub-lists share keys at the parent level.

func init() {
	Override(singletonWriter{
		family:        "ip_community_list",
		yangPath:      "/Cisco-IOS-XE-native:native/ip/community-list",
		envelopeKey:   "Cisco-IOS-XE-bgp:community-list",
		managedLeaves: []string{"standard", "expanded", "no-advertise"},
	})
}
