// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package iosxr

import "errors"

// ErrNotYetImplemented is the sentinel every IOS-XR code path
// returns until the platform's real implementation lands.
var ErrNotYetImplemented = errors.New("iosxr: driver not yet implemented")

// init is intentionally empty — the placeholder MUST NOT register.
// See drivers/nxos/register.go for the activation recipe; the same
// pattern applies here verbatim with DeviceDriverXR as the kind constant.
