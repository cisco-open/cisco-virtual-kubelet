// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package fmc

import (
	"context"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
)

func init() {
	drivers.Register(v1alpha1.DeviceDriverFMC,
		func(ctx context.Context, spec *v1alpha1.DeviceSpec) (drivers.CiscoKubernetesDeviceDriver, error) {
			return NewAppHostingDriver(ctx, spec)
		})

	// Register the configuration capability so controller/aggregator topology can
	// claim FMC devices. FMC uses provider.FMCConfigReconciler rather than the
	// IOS-XE YANG ConfigReconciler, so the context is intentionally empty.
	drivers.RegisterConfigDriver(v1alpha1.DeviceDriverFMC,
		func(context.Context, *v1alpha1.DeviceSpec, string, drivers.ConfigDriverOptions) (*drivers.ConfigDriverContext, error) {
			return &drivers.ConfigDriverContext{}, nil
		})
}
