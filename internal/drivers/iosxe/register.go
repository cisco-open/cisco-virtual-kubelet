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
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/iosxebuilder"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	log "github.com/virtual-kubelet/virtual-kubelet/log"
)

// init wires IOS-XE into both the apphosting and configdriver
// registries. The cisco-vk binary picks both up via the single
// blank import:
//
//	import _ "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe"
//
// Removing that blank import removes IOS-XE from the binary's
// supported set without any other source change.
func init() {
	drivers.Register(v1alpha1.DeviceDriverXE,
		func(ctx context.Context, spec *v1alpha1.DeviceSpec) (drivers.CiscoKubernetesDeviceDriver, error) {
			return NewAppHostingDriver(ctx, spec)
		})

	drivers.RegisterConfigDriver(v1alpha1.DeviceDriverXE, buildXEConfigDriverContext)

	// Wire the gNMI keyed-list registry through the production
	// startup path so writers' gNMI paths use the correct list-key
	// names. RegisterPathKey is idempotent.
	iosxebuilder.RegisterGNMIPathKeysForXE()
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
		Transport:             t,
		KeyRules:              iosxebuilder.KeyRulesForXE(),
		SupportedYANGVersions: supported,
		DefaultYANGVersion:    defaultVer,
		LookupWriter:          iosxebuilder.LookupWriter,
		SubscribePaths:        iosxebuilder.UnionWriterPaths(),
		FamilyOrder:           iosxebuilder.FamilyOrderForXE(),
	}
	if err != nil {
		return out, err
	}
	// Best-effort version fetch for version-aware writers. If the
	// transport dialled successfully we can reach the device.
	if ver := fetchDeviceVersion(ctx, t); ver != "" {
		out.DeviceVersion = ver
	}
	return out, nil
}

// fetchDeviceVersion makes a lightweight RESTCONF GET for the
// software-version field. Returns "" on any error.
func fetchDeviceVersion(ctx context.Context, t transport.Interface) string {
	if t == nil {
		return ""
	}
	raw, err := t.Fetch(ctx,
		"/restconf/data/Cisco-IOS-XE-device-hardware-oper:device-hardware-data/device-hardware/device-system-data")
	if err != nil {
		log.G(ctx).WithError(err).Warn("config driver: could not fetch device system data")
		return ""
	}
	// Response wraps in module prefix:
	// {"Cisco-IOS-XE-device-hardware-oper:device-system-data": {"software-version": "...", ...}}
	var outer map[string]json.RawMessage
	if err := json.Unmarshal(raw, &outer); err != nil {
		log.G(ctx).WithError(err).Warn("config driver: could not parse device system data")
		return ""
	}
	for _, innerRaw := range outer {
		var inner map[string]any
		if err := json.Unmarshal(innerRaw, &inner); err != nil {
			continue
		}
		if sv, ok := inner["software-version"].(string); ok {
			ver := extractVersion(sv)
			log.G(ctx).WithField("version", ver).Info("config driver: fetched device version")
			return ver
		}
	}
	log.G(ctx).Warn("config driver: software-version not found in device-system-data")
	return ""
}

var versionRe = regexp.MustCompile(`Version\s+(\d+\.\d+\.\d+)`)

func extractVersion(full string) string {
	m := versionRe.FindStringSubmatch(full)
	if len(m) > 1 {
		return m[1]
	}
	return full
}
