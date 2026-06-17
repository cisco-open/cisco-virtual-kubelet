// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package nxos is the NX-OS platform driver. It drives NX-OS app-hosting
// through NX-API CLI and registers DeviceDriverNXOS with the apphosting
// registry. It also registers the NX-OS config driver, which reconciles the
// NetAsCode NX-OS device-centric stripe through NX-API-backed fetch/apply/
// verify adapters.
package nxos
