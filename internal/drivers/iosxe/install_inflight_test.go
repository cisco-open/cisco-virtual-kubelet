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

package iosxe

import "testing"

// TryMarkInstallInFlight is the dedup primitive that prevents
// recoverMissingContainers from spawning duplicate background installs
// when GetPodStatus is called concurrently for the same pod.
func TestTryMarkInstallInFlight(t *testing.T) {
	d := &XEDriver{installInFlight: make(map[string]bool)}

	if !d.tryMarkInstallInFlight("appA") {
		t.Fatal("first mark for appA should succeed")
	}
	if d.tryMarkInstallInFlight("appA") {
		t.Fatal("second mark for appA must be rejected while still in flight")
	}
	// A different appID is independent.
	if !d.tryMarkInstallInFlight("appB") {
		t.Fatal("first mark for appB should succeed (different appID)")
	}

	// Clearing appA frees the slot for retry.
	d.clearInstallInFlight("appA")
	if !d.tryMarkInstallInFlight("appA") {
		t.Fatal("mark after clear should succeed")
	}

	// Clearing an unknown id is a no-op (map delete on missing key).
	d.clearInstallInFlight("never-set")
}
