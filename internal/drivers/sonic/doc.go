// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

// Package sonic implements Cisco SONiC support for Cisco Virtual Kubelet.
//
// SONiC is treated as an OpenConfig-first platform. The driver uses gNMI for
// health, topology, telemetry-ready state, and SONICConfig reconciliation. It
// deliberately advertises zero pod capacity because the tested SONiC image does
// not support Cisco app-hosting.
package sonic
