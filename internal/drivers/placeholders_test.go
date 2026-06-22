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

package drivers_test

import (
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"

	// Blank-importing the placeholder packages must NOT register
	// them — that's the contract. If a future change accidentally
	// adds a Register call to one of these, this test fires.
	_ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxr"
	_ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/openconfig"
)

// TestPlaceholderPackagesDoNotRegister pins the Phase-9 contract:
// blank-importing a placeholder package compiles but does not put
// the platform in the registry. A binary built with placeholder
// packages blank-imported still sees only what was actually
// implemented (XE, NXOS, and FAKE in the cisco-vk binary today).
func TestPlaceholderPackagesDoNotRegister(t *testing.T) {
	t.Parallel()
	for _, kind := range []v1alpha1.DeviceDriver{
		v1alpha1.DeviceDriverXR,
		v1alpha1.DeviceDriverOPENCONFIG,
	} {
		if drivers.Registered(kind) {
			t.Errorf("placeholder driver %q is registered; the placeholder must NOT register until a real implementation lands", kind)
		}
		if drivers.ConfigDriverRegistered(kind) {
			t.Errorf("placeholder config driver %q is registered; the placeholder must NOT register until a real implementation lands", kind)
		}
	}
}

// TestRegistryEnumeratesOnlyRealDrivers confirms RegisteredKinds()
// reflects the real registrations (whatever this test binary
// pulls in via init order). The drivers/registry_test.go suite
// resets the registry; this test runs in the _test variant
// package so it sees the real init-time registrations.
//
// In the test binary today, no platform packages are imported by
// this package's test set — the iosxe and fake registrations come
// from their own init() chains only when blank-imported. So the
// expectation here is "RegisteredKinds() returns a sorted list and
// does not panic". The cmd/cisco-vk binary's own tests cover the
// "iosxe + fake are present" assertion.
func TestRegistryEnumeratesOnlyRealDrivers(t *testing.T) {
	t.Parallel()
	got := drivers.RegisteredKinds()
	for _, k := range got {
		switch k {
		case v1alpha1.DeviceDriverXR,
			v1alpha1.DeviceDriverOPENCONFIG:
			t.Errorf("placeholder kind %q appears in RegisteredKinds()=%v", k, got)
		}
	}
}
