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

package configdriver

import (
	"errors"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// ErrNotImplemented marks methods that are still stubbed in the Phase-0
// scaffold. Callers should treat it as a distinct sentinel rather than a
// generic failure so status reporting can surface "feature not yet built"
// separately from "device returned an error".
var ErrNotImplemented = errors.New("iosxe config driver: not implemented in Phase 0 scaffold")

// DeviceInfo is the minimum metadata the driver needs to connect to a
// device. It is a value type rather than a pointer into the CiscoDevice
// CR so the driver can be exercised in tests without dragging in the API
// package at test time.
type DeviceInfo struct {
	// Name is the CiscoDevice metadata.name; doubles as the Kubernetes Node
	// name once the VK registers.
	Name string
	// Address is the management IP or hostname of the device.
	Address string
	// SoftwareVersion is the Cisco-IOS-XE-native:version reported by the
	// device on first connect. The schema toolchain uses this to pick a
	// compatible YANG release when the CR does not pin one.
	SoftwareVersion string
}

// ResolvedIntent is the normalised output of merging a device's
// IOSXEConfigDefaults, matching IOSXEDeviceGroupConfigs, expanded
// IOSXETemplate fragments, and the per-device IOSXEConfig. Merge order
// is fixed (defaults → groups → templates → per-device); rightmost wins
// per netascode semantics.
type ResolvedIntent struct {
	// DeviceName is the CiscoDevice the intent targets.
	DeviceName string
	// ManagedFamilies is the closed set of families the driver is allowed
	// to write on behalf of this CR.
	ManagedFamilies []string
	// Configuration is the merged netascode body, keyed by family name.
	// The map is deeply-structured plain-data — the driver never holds
	// a reference to the source CR so the CR can be mutated concurrently.
	Configuration map[string]any
	// Transactional mirrors the source CR's setting. Drivers that cannot
	// honour it (e.g. RESTCONF) downgrade with a recorded warning.
	Transactional bool
	// Source is a copy of the CR the intent was derived from. Provided for
	// status writes and event recording; never mutated by the driver.
	Source *configv1alpha1.IOSXEConfig
}

// Observed is the point-in-time snapshot the driver fetched from the device
// keyed by family. The map values are opaque to the driver core and
// interpreted by per-family writers.
type Observed struct {
	// YangVersion is the release the observed data was decoded against.
	YangVersion string
	// Families maps family name → opaque family-specific payload.
	Families map[string]any
}

// Plan is a per-family ordered list of device operations the driver intends
// to apply. PreImage captures the pre-change state so Rollback can replay
// it on a best-effort basis when the transport lacks a candidate datastore.
type Plan struct {
	// Family is the netascode family the plan acts on.
	Family string
	// Operations are executed in order; a failure short-circuits the plan.
	Operations []transport.Op
	// PreImage is opaque family payload captured before the apply; used by
	// Rollback on RESTCONF where no candidate datastore exists.
	PreImage map[string][]byte
}

// ApplyResult summarises the outcome of a Plan execution.
type ApplyResult struct {
	// Applied lists families whose operations all completed successfully.
	Applied []string
	// Failed lists families whose apply short-circuited with an error.
	Failed []FamilyError
	// Rollback is the token consumable by a subsequent Rollback call;
	// empty when no rollback is available (e.g. best-effort RESTCONF path).
	Rollback RollbackToken
}

// FamilyError is the structured form of a family apply failure.
type FamilyError struct {
	Family  string
	Message string
}

// RollbackToken is the opaque identifier returned by Apply; interpretation
// is driver-implementation-specific.
type RollbackToken string

// TransportClient is a type alias kept for source-compatibility in the
// configdriver package surface. New code should import
// "internal/drivers/iosxe/configdriver/transport" and use
// transport.Interface directly.
type TransportClient = transport.Interface
