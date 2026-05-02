// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package nxos is the NX-OS platform driver placeholder. It exists
// so the Phase-9 plug-in pattern is observable end-to-end: the
// directory layout, the register.go shape, and the Driver enum
// constant (DeviceDriverNXOS) are all in place, ready for the real
// implementation to land without any foundation edits.
//
// Status: not registered. Importing this package compiles but
// neither the apphosting nor the config-driver factories register
// — every entrypoint returns ErrNotYetImplemented. To activate:
//
//  1. Implement NewAppHostingDriver and the configdriver
//     equivalent (transport setup, family writers, schema).
//  2. Uncomment the drivers.Register / drivers.RegisterConfigDriver
//     calls in register.go below.
//  3. Add `_ "…/internal/drivers/nxos"` to
//     `cmd/cisco-vk/drivers_register.go`.
//
// See `docs/rfcs/driver-extension-guide.md` for the full
// walk-through.
package nxos
