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
	"sort"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Descriptor captures the stable orchestration metadata for a technology
// stripe. It mirrors the Network as Code distinction between device-centric,
// controller-centric, and solution-centric models while keeping execution
// anchored in per-device workers unless explicitly shifted by topology.
type Descriptor struct {
	Driver             ciskov1.DeviceDriver
	PlatformName       string
	NetAsCode          ciskov1.NetAsCodeModelStatus
	WorkerCapabilities []ciskov1.WorkerCapabilityName
}

var descriptors = map[ciskov1.DeviceDriver]Descriptor{
	ciskov1.DeviceDriverXE: {
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
	},
	ciskov1.DeviceDriverNXOS: {
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
		},
	},
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

// ForDriver returns the descriptor for a registered CiscoDevice driver.
func ForDriver(driver ciskov1.DeviceDriver) (Descriptor, bool) {
	d, ok := descriptors[driver]
	if !ok {
		return Descriptor{}, false
	}
	return cloneDescriptor(d), true
}

// KnownDrivers returns the driver keys that have descriptors.
func KnownDrivers() []ciskov1.DeviceDriver {
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
	return out
}
