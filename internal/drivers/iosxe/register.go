// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package iosxe

import (
	"context"
	"fmt"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/iosxebuilder"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
)

// init registers IOS-XE with both the apphosting driver registry
// (CiscoKubernetesDeviceDriver) and the configdriver registry
// (ConfigDriverContext) on the platform-agnostic side. The cisco-vk
// binary picks both up via the single blank import:
//
//	import _ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe"
//
// Removing that blank import removes IOS-XE from the binary's
// supported set without any other source change. Adding a new
// platform is the symmetric move: drop a register.go in the new
// package, blank-import it from the binary.
func init() {
	drivers.Register(v1alpha1.DeviceDriverXE,
		func(ctx context.Context, spec *v1alpha1.DeviceSpec) (drivers.CiscoKubernetesDeviceDriver, error) {
			return NewAppHostingDriver(ctx, spec)
		})

	drivers.RegisterConfigDriver(v1alpha1.DeviceDriverXE, buildXEConfigDriverContext)
}

// buildXEConfigDriverContext is the IOS-XE ConfigDriverFactory.
// Composes the platform-specific helpers in
// internal/drivers/iosxe/configdriver/ into a ConfigDriverContext
// the platform-agnostic provider.ConfigReconciler can consume.
func buildXEConfigDriverContext(
	ctx context.Context,
	spec *v1alpha1.DeviceSpec,
	password string,
	opts drivers.ConfigDriverOptions,
) (*drivers.ConfigDriverContext, error) {
	if spec == nil {
		return nil, fmt.Errorf("iosxe configdriver: nil spec")
	}
	t, err := transport.For(spec, password, transport.FactoryOptions{
		SessionLock: opts.SessionLock,
	})
	supported, defaultVer := iosxebuilder.LoadYANGReleaseTags(ctx)
	out := &drivers.ConfigDriverContext{
		Transport:             t, // may be nil if err != nil — caller decides whether to proceed
		KeyRules:              iosxebuilder.KeyRulesForXE(),
		SupportedYANGVersions: supported,
		DefaultYANGVersion:    defaultVer,
		LookupWriter:          iosxebuilder.LookupWriter,
		SubscribePaths:        iosxebuilder.UnionWriterPaths(),
	}
	// Surface transport-build failure but keep going — historical
	// behaviour is "scaffold mode": the reconciler runs, marks CRs
	// Pending, and recovers when the transport becomes reachable.
	if err != nil {
		return out, err
	}
	return out, nil
}
