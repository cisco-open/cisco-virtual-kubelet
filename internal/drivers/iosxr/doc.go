// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package iosxr implements IOS XR app-hosting through XR appmgr over SSH CLI.
//
// IOS XR appmgr manages Docker applications as RPM-backed sources. The driver
// therefore expects pod images to name an appmgr RPM already present on the
// router, for example /harddisk:/hello-app-0.1.0-ThinXR_26.1.1.x86_64.rpm, or
// to name a pre-installed appmgr source. The package is deliberately split into
// transport, lifecycle, parsing, and topology files so future IOS XR NetAsCode,
// gNOI, and telemetry work can extend the same management channel cleanly.
package iosxr
