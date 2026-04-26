// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package openconfig

import "errors"

// ErrNotYetImplemented is the sentinel every OpenConfig code path
// returns until the driver's real implementation lands.
var ErrNotYetImplemented = errors.New("openconfig: driver not yet implemented")

// init is intentionally empty — the placeholder MUST NOT register.
// See drivers/nxos/register.go for the activation recipe; the same
// pattern applies here verbatim with DeviceDriverOPENCONFIG as the
// kind constant. The reusable design choice when activating is to
// source family paths from schema's `openconfig_paths` rather than
// platform-specific `yang_paths`, so the writers can target any
// device that implements the OpenConfig YANG models.
