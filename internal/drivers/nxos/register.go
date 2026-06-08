// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package nxos

import (
	"context"
	"errors"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
)

// ErrNotYetImplemented is the sentinel every NX-OS code path
// returns until the platform's real implementation lands. Keeping
// the package importable means structural changes in the
// foundation (registry signatures, ConfigDriverContext fields)
// surface here as compilation failures and the placeholder stays
// honest with the API.
var ErrNotYetImplemented = errors.New("nxos: driver not yet implemented")

// init wires NX-OS into the apphosting registry. NX-OS declarative config
// writers are intentionally not registered yet; the configdriver registry stays
// empty for this kind until a real NX-OS writer set lands.
func init() {
	drivers.Register(v1alpha1.DeviceDriverNXOS,
		func(ctx context.Context, spec *v1alpha1.DeviceSpec) (drivers.CiscoKubernetesDeviceDriver, error) {
			return NewAppHostingDriver(ctx, spec)
		})
}
