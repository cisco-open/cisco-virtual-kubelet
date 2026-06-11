// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package fmc implements the Cisco Secure Firewall Management Center driver.
// The driver exposes FMC as a health, operations, telemetry, and declarative
// configuration node. FMC does not support Cisco app-hosting, so workload pod
// capacity is deliberately advertised as zero.
package fmc
