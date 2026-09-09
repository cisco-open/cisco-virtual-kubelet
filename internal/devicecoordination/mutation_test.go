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

package devicecoordination

import "testing"

func TestDeviceKeyScopesNamespaceAndDevice(t *testing.T) {
	key := DeviceKey("tenant-a", "edge-1")
	if key == DeviceKey("tenant-b", "edge-1") || key == DeviceKey("tenant-a", "edge-2") {
		t.Fatal("device key did not include both namespace and name")
	}
	if len(key) > 63 {
		t.Fatalf("device key %q is not label-safe", key)
	}
}

func TestHolderIdentityUsesObjectUID(t *testing.T) {
	first := HolderIdentity("software-upgrade", "default", "upgrade", "uid-1")
	second := HolderIdentity("software-upgrade", "default", "upgrade", "uid-2")
	if first == second {
		t.Fatal("recreated objects share a holder identity")
	}
	if got, want := HolderIdentity("software-upgrade", "default", "upgrade", ""), HolderIdentity("software-upgrade", "default", "upgrade", ""); got != want {
		t.Fatal("UID-less fallback identity is not stable")
	}
}
