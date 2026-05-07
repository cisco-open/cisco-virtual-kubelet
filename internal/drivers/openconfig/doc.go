// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package openconfig is the OpenConfig-YANG cross-vendor driver
// placeholder. It exists so the Phase-9 plug-in pattern is observable
// end-to-end: the directory layout, the register.go shape, and the
// Driver enum constant (DeviceDriverOPENCONFIG) are all in place,
// ready for the real implementation to land without any foundation
// edits.
//
// Why "openconfig" and not a vendor name. The shipped per-vendor
// placeholders — `iosxr/`, `nxos/` — exist for Cisco platforms whose
// real drivers will speak the platform's native YANG (Cisco-IOS-XR-*,
// Cisco-NX-OS-*) over the platform-preferred transport. The
// `openconfig/` placeholder is intentionally different: it targets
// the vendor-neutral OpenConfig YANG models (openconfig-interfaces,
// openconfig-network-instance, openconfig-bgp, …) over NETCONF or
// gNMI. A device that implements OpenConfig — Cisco IOS-XE/IOS-XR
// in OpenConfig mode, Juniper Junos, Arista EOS, Nokia SR Linux,
// and most modern vendor NOSes — can be reconciled by this driver
// without a per-vendor writer set.
//
// The configdriver schema already declares an `openconfig_paths`
// alongside `yang_paths` for every shipped family
// (`internal/drivers/iosxe/configdriver/schema/families.yaml`). When
// the OpenConfig driver lands it will read those `openconfig_paths`
// instead of the per-platform `yang_paths`, so the family registry
// is reusable rather than re-implemented.
//
// Status: not registered. Importing this package compiles but
// neither the apphosting nor the config-driver factories register
// — every entrypoint returns ErrNotYetImplemented. To activate:
//
//  1. Implement NewAppHostingDriver and the configdriver equivalent
//     using OpenConfig YANG paths (the configdriver Resolver +
//     KeyRules can be reused by switching the path source from
//     `yang_paths` to `openconfig_paths`). Pick gNMI or NETCONF
//     as the transport — both are standardised against OpenConfig.
//  2. Uncomment the drivers.Register / drivers.RegisterConfigDriver
//     calls in register.go below.
//  3. Add `_ "…/internal/drivers/openconfig"` to
//     `cmd/cisco-vk/drivers_register.go`.
//
// See `docs/rfcs/driver-extension-guide.md` for the full
// walk-through.
package openconfig
