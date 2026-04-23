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

// Embedded Event Manager (EEM) Phase-3 writer. EEM applets are
// site-specific and often include embedded scripts; Phase-3
// manages the container as a singleton with the common leaves.

func init() {
	Override(singletonWriter{
		family:      "event_manager",
		yangPath:    "/Cisco-IOS-XE-native:native/event/manager",
		envelopeKey: "Cisco-IOS-XE-eem:manager",
		managedLeaves: []string{
			"applet",
			"environment",
			"session",
		},
	})
}
