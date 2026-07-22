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

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/iosxebuilder"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/transport"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/validation"
	iosxewriters "github.com/cisco/virtual-kubelet-cisco/internal/drivers/iosxe/configdriver/writers"
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
	validationMode, validationModeErr := validation.ModeFromEnv()
	if validationModeErr != nil {
		log.G(ctx).WithError(validationModeErr).Warn("config driver: disabling YANG validation")
	}
	out := &drivers.ConfigDriverContext{
		PlatformName:          "iosxe",
		ModelFormat:           configv1alpha1.NetAsCodeModelFormatIOSXE,
		ConfigObject:          &configv1alpha1.IOSXEConfig{},
		ConfigList:            &configv1alpha1.IOSXEConfigList{},
		Transport:             t,
		KeyRules:              iosxebuilder.KeyRulesForXE(),
		SupportedYANGVersions: supported,
		DefaultYANGVersion:    defaultVer,
		LookupWriter:          iosxebuilder.LookupWriter,
		SubscribePaths:        iosxebuilder.UnionWriterPaths(),
		FetchDeviceVersion:    FetchDeviceVersion,
		DeviceVersionPolicy: drivers.DeviceVersionPolicy{
			Validate:      iosxewriters.SetDeviceVersion,
			IsUnsupported: iosxewriters.IsUnsupportedDeviceVersion,
			IsMalformed:   iosxewriters.IsMalformedDeviceVersion,
			ReleaseTag:    iosxewriters.ReleaseTagForDeviceVersionString,
			Require:       true,
		},
		FamilyOrder:             iosxebuilder.FamilyOrderForXE(),
		OperationValidator:      validation.NewStructuralValidator(),
		OperationValidationMode: validationMode,
	}
	if err != nil {
		return out, err
	}
	// Best-effort version fetch for version-aware writers. If the
	// transport dialled successfully we can reach the device.
	if ver := FetchDeviceVersion(ctx, t); ver != "" {
		out.DeviceVersion = ver
	}
	return out, nil
}

// FetchDeviceVersion makes a lightweight RESTCONF GET for the
// software-version field. Returns "" on any error.
//
// Exported so cisco-vk's deferred-dial retry loop can rebind the
// device version after a startup race recovers — see the comment in
// cmd/cisco-vk/config_reconciler.go retryConfigDriverDial.
func FetchDeviceVersion(ctx context.Context, t transport.Interface) string {
	if t == nil {
		return ""
	}
	raw, err := t.Fetch(ctx,
		"/Cisco-IOS-XE-native:native/version")
	if err != nil {
		log.G(ctx).WithError(err).Warn("config driver: could not fetch device version")
		return ""
	}
	// Response: {"Cisco-IOS-XE-native:version": "17.16"}
	var envelope map[string]string
	if err := json.Unmarshal(raw, &envelope); err != nil {
		log.G(ctx).WithError(err).Warn("config driver: could not parse version response")
		return ""
	}
	for _, v := range envelope {
		log.G(ctx).WithField("version", v).Info("config driver: fetched device version")
		return v
	}
	log.G(ctx).Warn("config driver: empty version response")
	return ""
}
