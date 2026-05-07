// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package nxos

import (
	"errors"
)

// ErrNotYetImplemented is the sentinel every NX-OS code path
// returns until the platform's real implementation lands. Keeping
// the package importable means structural changes in the
// foundation (registry signatures, ConfigDriverContext fields)
// surface here as compilation failures and the placeholder stays
// honest with the API.
var ErrNotYetImplemented = errors.New("nxos: driver not yet implemented")

// init is intentionally empty — the placeholder MUST NOT register.
// A binary that blank-imports this package today will see
// "driver kind \"NXOS\" is not registered" if a CiscoDevice with
// that kind is created, which is exactly what we want until the
// real implementation lands.
//
// When the real driver lands, replace this comment with:
//
//	func init() {
//	  drivers.Register(v1alpha1.DeviceDriverNXOS,
//	    func(ctx, spec) (drivers.CiscoKubernetesDeviceDriver, error) {
//	      return NewAppHostingDriver(ctx, spec)
//	    })
//	  drivers.RegisterConfigDriver(v1alpha1.DeviceDriverNXOS,
//	    buildNXOSConfigDriverContext)
//	}
//
// then drop a blank import in cmd/cisco-vk/drivers_register.go.
