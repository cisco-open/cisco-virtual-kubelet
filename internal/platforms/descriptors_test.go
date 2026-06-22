// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package platforms

import (
	"slices"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestDescriptorRuntimeSurfaces(t *testing.T) {
	xe, ok := ForDriver(ciskov1.DeviceDriverXE)
	if !ok {
		t.Fatal("XE descriptor missing")
	}
	if xe.Config.Kind != ConfigKindIOSXE || xe.Config.Runtime != ConfigRuntimeIOSXE {
		t.Fatalf("XE config surface=%+v, want IOSXE compatibility runtime", xe.Config)
	}
	if xe.ConfigPrereqs.Kind != ConfigKindIOSXE || len(xe.ConfigPrereqs.FixedManagedFamilies) == 0 {
		t.Fatalf("XE prereq policy=%+v, want fixed IOSXEConfig policy", xe.ConfigPrereqs)
	}

	nxos, ok := ForDriver(ciskov1.DeviceDriverNXOS)
	if !ok {
		t.Fatal("NXOS descriptor missing")
	}
	if nxos.Config.Kind != ConfigKindNXOS || nxos.Config.Runtime != ConfigRuntimeCommon {
		t.Fatalf("NXOS config surface=%+v, want NXOSConfig on common runtime", nxos.Config)
	}
	if !nxos.ConfigPrereqs.DeriveManagedFamiliesFromSource {
		t.Fatalf("NXOS prereq policy=%+v, want source-derived families", nxos.ConfigPrereqs)
	}
	if !slices.Contains(nxos.Runtime.Transports, TransportKindREST) {
		t.Fatalf("NXOS transports=%v, want neutral rest transport", nxos.Runtime.Transports)
	}
	for _, capability := range []ciskov1.WorkerCapabilityName{
		ciskov1.WorkerCapabilityDiagnostics,
		ciskov1.WorkerCapabilityOperations,
	} {
		if !slices.Contains(nxos.WorkerCapabilities, capability) {
			t.Fatalf("NXOS worker capabilities=%v, missing %s", nxos.WorkerCapabilities, capability)
		}
	}
}

func TestRegisterRejectsDuplicateDescriptor(t *testing.T) {
	err := Register(Descriptor{
		Driver:       ciskov1.DeviceDriverXE,
		PlatformName: "iosxe-duplicate",
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("Register duplicate error=%v, want duplicate rejection", err)
	}
}

func TestConfigPrereqSupportedFamiliesStayWithinConfigSurface(t *testing.T) {
	for _, driver := range KnownDrivers() {
		d, ok := ForDriver(driver)
		if !ok {
			t.Fatalf("ForDriver(%s) not found", driver)
		}
		for _, family := range d.ConfigPrereqs.SupportedManagedFamilies {
			if !slices.Contains(d.Config.Families, family) {
				t.Fatalf("%s prereq supported family %q not present in config surface %v",
					driver, family, d.Config.Families)
			}
		}
	}
}

func TestForDriverReturnsDescriptorCopy(t *testing.T) {
	first, ok := ForDriver(ciskov1.DeviceDriverNXOS)
	if !ok {
		t.Fatal("NXOS descriptor missing")
	}
	first.Config.Families[0] = "mutated"
	first.ConfigPrereqs.SupportedManagedFamilies[0] = "mutated"
	first.WorkerCapabilities[0] = "mutated"
	first.Runtime.Transports[0] = "mutated"

	second, ok := ForDriver(ciskov1.DeviceDriverNXOS)
	if !ok {
		t.Fatal("NXOS descriptor missing on second read")
	}
	if second.Config.Families[0] == "mutated" ||
		second.ConfigPrereqs.SupportedManagedFamilies[0] == "mutated" ||
		second.WorkerCapabilities[0] == "mutated" ||
		second.Runtime.Transports[0] == "mutated" {
		t.Fatalf("ForDriver returned shared mutable descriptor state: %+v", second)
	}
}

func TestForDriverNetAsCodeDeviceCentricStripes(t *testing.T) {
	for _, driver := range []ciskov1.DeviceDriver{ciskov1.DeviceDriverXE, ciskov1.DeviceDriverNXOS} {
		d, ok := ForDriver(driver)
		if !ok {
			t.Fatalf("ForDriver(%s) not found", driver)
		}
		if d.NetAsCode.Type != ciskov1.NetAsCodeModelDeviceCentric {
			t.Fatalf("%s NetAsCode type=%q, want %q", driver, d.NetAsCode.Type, ciskov1.NetAsCodeModelDeviceCentric)
		}
		for _, section := range []string{"entity", "device", "interface"} {
			if !slices.Contains(d.NetAsCode.Sections, section) {
				t.Fatalf("%s sections=%v missing %q", driver, d.NetAsCode.Sections, section)
			}
		}
	}
}

func TestWorkerCapabilityStatusesAggregatedIsConfigOnly(t *testing.T) {
	statuses := WorkerCapabilityStatuses(ciskov1.DeviceDriverXE, ciskov1.WorkerTopologyAggregated)
	if len(statuses) == 0 {
		t.Fatal("expected XE capabilities")
	}
	for _, status := range statuses {
		if status.Name == ciskov1.WorkerCapabilityConfig {
			if !status.Enabled || status.Runtime != ciskov1.WorkerRuntimeAggregator {
				t.Fatalf("config status=%+v, want enabled on aggregator", status)
			}
			continue
		}
		if status.Enabled {
			t.Fatalf("%s enabled in aggregated topology; want config-only capabilities", status.Name)
		}
	}
}

func TestNetAsCodeCatalogIncludesControllerAndSolutionStripes(t *testing.T) {
	for _, stripe := range []string{
		"fmc",
		"ftd",
		"ise",
		"apic",
		"catalyst_center",
		"meraki",
		"ndo",
		"hyperfabric",
		"vxlan",
	} {
		model, ok := NetAsCodeCatalog[stripe]
		if !ok {
			t.Fatalf("NetAsCodeCatalog missing %q", stripe)
		}
		if model.Stripe != stripe {
			t.Fatalf("%s stripe=%q", stripe, model.Stripe)
		}
		if len(model.Sections) == 0 {
			t.Fatalf("%s has empty section list", stripe)
		}
	}
	if NetAsCodeCatalog["vxlan"].Type != ciskov1.NetAsCodeModelSolutionCentric {
		t.Fatalf("vxlan type=%q, want solution-centric", NetAsCodeCatalog["vxlan"].Type)
	}
	if NetAsCodeCatalog["ise"].Type != ciskov1.NetAsCodeModelControllerCentric {
		t.Fatalf("ise type=%q, want controller-centric", NetAsCodeCatalog["ise"].Type)
	}
	if NetAsCodeCatalog["ftd"].Format != "netascode-fmc" {
		t.Fatalf("ftd format=%q, want FMC-managed model", NetAsCodeCatalog["ftd"].Format)
	}
}

func TestNetAsCodeCatalogKeepsSONICOpenConfigUntilNativeStripeExists(t *testing.T) {
	model, ok := NetAsCodeCatalog["sonic"]
	if !ok {
		t.Fatal("NetAsCodeCatalog missing sonic placeholder")
	}
	if model.Format != "openconfig" {
		t.Fatalf("sonic format=%q, want openconfig", model.Format)
	}
	if model.Type != ciskov1.NetAsCodeModelDeviceCentric {
		t.Fatalf("sonic type=%q, want device-centric", model.Type)
	}
}
