// Copyright © 2026 Cisco Systems, Inc.
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

package main

// This file is the cisco-vk binary's platform-registration hub.
// Adding a new platform (NX-OS, IOSXR, Junos, …) is a one-line
// change here — drop a blank import for the new platform package
// and the driver registry picks it up via init() side effect. No
// other source file in the binary needs to change.
//
// See docs/rfcs/driver-extension-guide.md for the end-to-end
// "how to add a platform" walk-through.

import (
	// IOS-XE: the established platform.
	_ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe"

	// FAKE: in-memory test driver. Always registered so
	// integration tests can target Driver=FAKE without a real
	// device; production deployments simply never set that on a
	// CiscoDevice.
	_ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/fake"
	// Future platforms: the intent of Phase 9 is that adding NX-OS,
	// IOSXR, or Junos here is the only edit needed in the binary.
	// Placeholder packages exist under internal/drivers/<name>/ but
	// stay un-imported until a real implementation lands.
	//
	// _ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos"
	// _ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxr"
	// _ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/junos"
)
