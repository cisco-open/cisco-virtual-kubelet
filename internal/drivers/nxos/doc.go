// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package nxos is the NX-OS platform driver. It drives NX-OS app-hosting
// through NX-API CLI and registers DeviceDriverNXOS with the apphosting
// registry.
//
// Status: apphosting is registered; configdriver is intentionally not
// registered until NX-OS-specific transport/schema/writer support lands.
package nxos
