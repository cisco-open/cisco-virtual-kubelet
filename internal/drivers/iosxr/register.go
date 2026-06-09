// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package iosxr

import (
	"context"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
)

func init() {
	drivers.Register(v1alpha1.DeviceDriverXR,
		func(ctx context.Context, spec *v1alpha1.DeviceSpec) (drivers.CiscoKubernetesDeviceDriver, error) {
			return NewAppHostingDriver(ctx, spec)
		})
}
