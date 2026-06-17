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
	"fmt"
	"strings"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
	nxoswriters "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/writers"
)

// init wires NX-OS into both the apphosting and configdriver registries.
func init() {
	drivers.Register(v1alpha1.DeviceDriverNXOS,
		func(ctx context.Context, spec *v1alpha1.DeviceSpec) (drivers.CiscoKubernetesDeviceDriver, error) {
			return NewAppHostingDriver(ctx, spec)
		})

	drivers.RegisterConfigDriver(v1alpha1.DeviceDriverNXOS, buildNXOSConfigDriverContext)
}

func buildNXOSConfigDriverContext(
	ctx context.Context,
	spec *v1alpha1.DeviceSpec,
	password string,
	opts drivers.ConfigDriverOptions,
) (*drivers.ConfigDriverContext, error) {
	if spec == nil {
		return nil, fmt.Errorf("nxos configdriver: nil spec")
	}
	specCopy := *spec
	if password != "" {
		specCopy.Password = password
	}
	fetchPaths := append([]string(nil), nxosschema.FetchPaths...)
	out := &drivers.ConfigDriverContext{
		PlatformName:       "nxos",
		ModelFormat:        configv1alpha1.NetAsCodeModelFormatNXOS,
		ConfigObject:       &configv1alpha1.NXOSConfig{},
		ConfigList:         &configv1alpha1.NXOSConfigList{},
		LookupWriter:       nxoswriters.GetForRelease,
		SubscribePaths:     fetchPaths,
		FetchDeviceVersion: FetchDeviceVersion,
		FamilyOrder:        nxosschema.FamilyOrder,
	}
	t, err := buildNXOSConfigTransport(&specCopy, opts)
	if err != nil {
		return out, err
	}
	out.Transport = t
	if ver := FetchDeviceVersion(ctx, t); ver != "" {
		out.DeviceVersion = ver
		out.DefaultYANGVersion = ver
	}
	return out, nil
}

func buildNXOSConfigTransport(spec *v1alpha1.DeviceSpec, opts drivers.ConfigDriverOptions) (transport.Interface, error) {
	kind := strings.ToLower(strings.TrimSpace(spec.Transport))
	switch kind {
	case "", string(transport.KindRESTCONF), string(transport.KindNXAPI):
		return newNXAPIConfigTransportWithOptions(spec, NXAPIConfigTransportOptions{
			SessionLock: opts.SessionLock,
		})
	case string(transport.KindNETCONF), string(transport.KindGNMI):
		return nil, fmt.Errorf("nxos configdriver: transport %q is not implemented yet; supported transport is %q", spec.Transport, transport.KindNXAPI)
	default:
		return nil, fmt.Errorf("nxos configdriver: unknown transport %q; supported transport is %q", spec.Transport, transport.KindNXAPI)
	}
}
