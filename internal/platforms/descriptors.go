// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package platforms records platform metadata that is useful to orchestration
// and status code without importing concrete driver packages. Concrete drivers
// still own their transports and writers; this package is intentionally
// descriptive, not executable.
package platforms

import (
	"fmt"
	"sort"
	"sync"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// ConfigRuntime names the reconciler runtime used by a platform config CRD.
// IOS-XE still has a compatibility runtime because it carries legacy support
// APIs; newer platform CRDs should use the common runtime.
type ConfigRuntime string

const (
	ConfigRuntimeNone   ConfigRuntime = ""
	ConfigRuntimeIOSXE  ConfigRuntime = "iosxe"
	ConfigRuntimeCommon ConfigRuntime = "common"
)

// ConfigKind names a platform-specific desired-config CRD.
type ConfigKind string

const (
	ConfigKindIOSXE ConfigKind = "IOSXEConfig"
	ConfigKindNXOS  ConfigKind = "NXOSConfig"
)

// TransportKind is a neutral transport family, not a platform product name.
type TransportKind string

const (
	TransportKindREST     TransportKind = "rest"
	TransportKindRESTCONF TransportKind = "restconf"
	TransportKindNETCONF  TransportKind = "netconf"
	TransportKindGNMI     TransportKind = "gnmi"
	TransportKindSSH      TransportKind = "ssh"
)

// ConfigSurface describes the config CRD and runtime for one platform.
type ConfigSurface struct {
	Kind        ConfigKind
	ListKind    string
	Runtime     ConfigRuntime
	ModelFormat configv1alpha1.NetAsCodeModelFormat
	Families    []string
}

// ConfigPrereqPolicy describes how CiscoDevice.spec.configPrereqs materializes
// the owned platform config CR.
type ConfigPrereqPolicy struct {
	Kind                            ConfigKind
	FixedManagedFamilies            []string
	SupportedManagedFamilies        []string
	DeriveManagedFamiliesFromSource bool
	EmptyIntentJSON                 string
}

// RuntimeSurfaces records optional runtime providers exposed by a platform.
// Values are descriptive provider names so the controller can report
// capability shape without importing concrete driver implementations.
type RuntimeSurfaces struct {
	Transports        []TransportKind
	Diagnostics       string
	Telemetry         string
	Lifecycle         string
	AppHosting        string
	Software          string
	WriteClassActions string
}

// Descriptor captures the stable orchestration metadata for a technology
// stripe. It mirrors the Network as Code distinction between device-centric,
// controller-centric, and solution-centric models while keeping execution
// anchored in per-device workers unless explicitly shifted by topology.
type Descriptor struct {
	Driver             ciskov1.DeviceDriver
	PlatformName       string
	NetAsCode          ciskov1.NetAsCodeModelStatus
	WorkerCapabilities []ciskov1.WorkerCapabilityName
	Config             ConfigSurface
	ConfigPrereqs      ConfigPrereqPolicy
	Runtime            RuntimeSurfaces
}

var (
	descriptorMu sync.RWMutex
	descriptors  = map[ciskov1.DeviceDriver]Descriptor{}
)

func init() {
	MustRegister(Descriptor{
		Driver:       ciskov1.DeviceDriverXE,
		PlatformName: "iosxe",
		NetAsCode: ciskov1.NetAsCodeModelStatus{
			Type:     ciskov1.NetAsCodeModelDeviceCentric,
			Format:   string(configv1alpha1.NetAsCodeModelFormatIOSXE),
			Stripe:   "iosxe",
			Sections: []string{"entity", "device", "interface"},
		},
		WorkerCapabilities: []ciskov1.WorkerCapabilityName{
			ciskov1.WorkerCapabilityAppHosting,
			ciskov1.WorkerCapabilityConfig,
			ciskov1.WorkerCapabilityTelemetry,
			ciskov1.WorkerCapabilityDiagnostics,
			ciskov1.WorkerCapabilityOperations,
			ciskov1.WorkerCapabilityLifecycle,
		},
		Config: ConfigSurface{
			Kind:        ConfigKindIOSXE,
			ListKind:    "IOSXEConfigList",
			Runtime:     ConfigRuntimeIOSXE,
			ModelFormat: configv1alpha1.NetAsCodeModelFormatIOSXE,
			Families: []string{
				"interface_virtual_port_group",
				"dhcp",
				"access_list_extended",
			},
		},
		ConfigPrereqs: ConfigPrereqPolicy{
			Kind:                 ConfigKindIOSXE,
			FixedManagedFamilies: []string{"interface_virtual_port_group", "dhcp", "access_list_extended"},
			EmptyIntentJSON:      `{"interface_virtual_port_group":{},"dhcp":{},"access_list_extended":{}}`,
		},
		Runtime: RuntimeSurfaces{
			Transports:        []TransportKind{TransportKindRESTCONF, TransportKindNETCONF, TransportKindGNMI},
			Diagnostics:       "iosxe-diagnostic",
			Telemetry:         "iosxe-mdt-gnmi",
			Lifecycle:         "iosxe-gnoi",
			AppHosting:        "iosxe-app-hosting",
			Software:          "iosxe-gnoi-os",
			WriteClassActions: "iosxe-gnoi-explicit-opt-in",
		},
	})
	MustRegister(Descriptor{
		Driver:       ciskov1.DeviceDriverNXOS,
		PlatformName: "nxos",
		NetAsCode: ciskov1.NetAsCodeModelStatus{
			Type:     ciskov1.NetAsCodeModelDeviceCentric,
			Format:   string(configv1alpha1.NetAsCodeModelFormatNXOS),
			Stripe:   "nxos",
			Sections: []string{"entity", "device", "interface"},
		},
		WorkerCapabilities: []ciskov1.WorkerCapabilityName{
			ciskov1.WorkerCapabilityAppHosting,
			ciskov1.WorkerCapabilityConfig,
			ciskov1.WorkerCapabilityDiagnostics,
			ciskov1.WorkerCapabilityOperations,
		},
		Config: ConfigSurface{
			Kind:        ConfigKindNXOS,
			ListKind:    "NXOSConfigList",
			Runtime:     ConfigRuntimeCommon,
			ModelFormat: configv1alpha1.NetAsCodeModelFormatNXOS,
			Families: []string{
				"system",
				"feature",
				"feature_set",
				"vlan",
				"interface_ethernet",
			},
		},
		ConfigPrereqs: ConfigPrereqPolicy{
			Kind:                            ConfigKindNXOS,
			SupportedManagedFamilies:        []string{"system", "feature", "feature_set", "vlan", "interface_ethernet"},
			DeriveManagedFamiliesFromSource: true,
			EmptyIntentJSON:                 `{}`,
		},
		Runtime: RuntimeSurfaces{
			Transports:  []TransportKind{TransportKindREST},
			Diagnostics: "nxos-diagnostic-nxapi-cli",
			AppHosting:  "nxos-app-hosting",
		},
	})
}

// NetAsCodeCatalog records model stripes that are planned but not necessarily
// registered as CiscoDevice drivers yet. This keeps future controller-centric
// platforms aligned to the upstream Network as Code sections without implying
// runtime support before a platform package lands.
var NetAsCodeCatalog = map[string]ciskov1.NetAsCodeModelStatus{
	"apic": {
		Type:     ciskov1.NetAsCodeModelControllerCentric,
		Format:   "netascode-apic",
		Stripe:   "apic",
		Sections: []string{"bootstrap", "access", "fabric", "interface", "node", "pod", "tenant"},
	},
	"fmc": {
		Type:     ciskov1.NetAsCodeModelControllerCentric,
		Format:   "netascode-fmc",
		Stripe:   "fmc",
		Sections: []string{"nac_configuration", "existing_configuration", "system", "domains", "devices", "objects", "policies", "vpn"},
	},
	"ftd": {
		Type:     ciskov1.NetAsCodeModelControllerCentric,
		Format:   "netascode-fmc",
		Stripe:   "ftd",
		Sections: []string{"fmc-managed-devices", "objects", "policies", "deployments", "policy-assignment"},
	},
	"catalyst_center": {
		Type:     ciskov1.NetAsCodeModelControllerCentric,
		Format:   "netascode-catalyst-center",
		Stripe:   "catalyst_center",
		Sections: []string{"sites", "fabric", "wireless", "inventory", "templates"},
	},
	"hyperfabric": {
		Type:     ciskov1.NetAsCodeModelSolutionCentric,
		Format:   "netascode-hyperfabric",
		Stripe:   "hyperfabric",
		Sections: []string{"system", "fabric"},
	},
	"iosxr": {
		Type:     ciskov1.NetAsCodeModelDeviceCentric,
		Format:   "netascode-iosxr",
		Stripe:   "iosxr",
		Sections: []string{"entity", "device", "interface"},
	},
	"ise": {
		Type:     ciskov1.NetAsCodeModelControllerCentric,
		Format:   "netascode-ise",
		Stripe:   "ise",
		Sections: []string{"identity_management", "network_resources", "network_access", "device_administration", "trustsec", "system"},
	},
	"meraki": {
		Type:     ciskov1.NetAsCodeModelControllerCentric,
		Format:   "netascode-meraki",
		Stripe:   "meraki",
		Sections: []string{"organizations", "networks", "devices", "templates"},
	},
	"ndo": {
		Type:     ciskov1.NetAsCodeModelControllerCentric,
		Format:   "netascode-ndo",
		Stripe:   "ndo",
		Sections: []string{"system", "schema", "tenant_templates"},
	},
	"sonic": {
		Type:     ciskov1.NetAsCodeModelDeviceCentric,
		Format:   "openconfig",
		Stripe:   "sonic",
		Sections: []string{"openconfig"},
	},
	"sdwan": {
		Type:     ciskov1.NetAsCodeModelControllerCentric,
		Format:   "netascode-sdwan",
		Stripe:   "sdwan",
		Sections: []string{"configuration_groups", "policy_groups", "feature_profiles", "features", "policies", "policy_objects", "sites", "templates"},
	},
	"vxlan": {
		Type:     ciskov1.NetAsCodeModelSolutionCentric,
		Format:   "netascode-vxlan",
		Stripe:   "vxlan",
		Sections: []string{"fabric", "multisite", "global", "topology", "underlay", "overlay", "policy"},
	},
}

// MustRegister installs a platform descriptor and panics on invalid or
// duplicate registrations. Platform packages can call it from init() so future
// stripes do not need to edit controller startup code.
func MustRegister(d Descriptor) {
	if err := Register(d); err != nil {
		panic(err)
	}
}

// Register installs a platform descriptor.
func Register(d Descriptor) error {
	if d.Driver == "" {
		return fmt.Errorf("platform descriptor: empty driver")
	}
	if d.PlatformName == "" {
		return fmt.Errorf("platform descriptor %s: empty platform name", d.Driver)
	}
	if d.Config.Kind != "" && d.ConfigPrereqs.Kind == "" {
		d.ConfigPrereqs.Kind = d.Config.Kind
	}
	descriptorMu.Lock()
	defer descriptorMu.Unlock()
	if _, dup := descriptors[d.Driver]; dup {
		return fmt.Errorf("platform descriptor: duplicate registration for %q", d.Driver)
	}
	descriptors[d.Driver] = cloneDescriptor(d)
	return nil
}

// ForDriver returns the descriptor for a registered CiscoDevice driver.
func ForDriver(driver ciskov1.DeviceDriver) (Descriptor, bool) {
	descriptorMu.RLock()
	defer descriptorMu.RUnlock()
	d, ok := descriptors[driver]
	if !ok {
		return Descriptor{}, false
	}
	return cloneDescriptor(d), true
}

// KnownDrivers returns the driver keys that have descriptors.
func KnownDrivers() []ciskov1.DeviceDriver {
	descriptorMu.RLock()
	defer descriptorMu.RUnlock()
	out := make([]ciskov1.DeviceDriver, 0, len(descriptors))
	for driver := range descriptors {
		out = append(out, driver)
	}
	sort.Slice(out, func(i, j int) bool { return string(out[i]) < string(out[j]) })
	return out
}

// WorkerCapabilityStatuses renders the descriptor capabilities for the current
// topology. Aggregator mode only reports config because the current aggregator
// owns only config reconciliation; device-local telemetry, lifecycle, and debug
// actions remain a per-device worker concern.
func WorkerCapabilityStatuses(
	driver ciskov1.DeviceDriver,
	topology ciskov1.WorkerTopology,
) []ciskov1.WorkerCapabilityStatus {
	d, ok := ForDriver(driver)
	if !ok {
		return nil
	}
	out := make([]ciskov1.WorkerCapabilityStatus, 0, len(d.WorkerCapabilities))
	for _, cap := range d.WorkerCapabilities {
		status := ciskov1.WorkerCapabilityStatus{Name: cap, Enabled: true}
		switch topology {
		case ciskov1.WorkerTopologyAggregated:
			if cap == ciskov1.WorkerCapabilityConfig {
				status.Runtime = ciskov1.WorkerRuntimeAggregator
			} else {
				status.Enabled = false
				status.Message = "disabled while manager aggregator owns config-only topology"
			}
		case ciskov1.WorkerTopologyPerDevice:
			status.Runtime = ciskov1.WorkerRuntimePerDeviceWorker
		}
		out = append(out, status)
	}
	return out
}

func cloneDescriptor(in Descriptor) Descriptor {
	out := in
	out.NetAsCode.Sections = append([]string(nil), in.NetAsCode.Sections...)
	out.WorkerCapabilities = append([]ciskov1.WorkerCapabilityName(nil), in.WorkerCapabilities...)
	out.Config.Families = append([]string(nil), in.Config.Families...)
	out.ConfigPrereqs.FixedManagedFamilies = append([]string(nil), in.ConfigPrereqs.FixedManagedFamilies...)
	out.ConfigPrereqs.SupportedManagedFamilies = append([]string(nil), in.ConfigPrereqs.SupportedManagedFamilies...)
	out.Runtime.Transports = append([]TransportKind(nil), in.Runtime.Transports...)
	return out
}
