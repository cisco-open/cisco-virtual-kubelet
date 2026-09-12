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

package main

import (
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/client-go/rest"
)

func TestMaintenanceRuntimeBoundary(t *testing.T) {
	for _, tc := range []struct {
		name                                       string
		driver                                     ciskov1.DeviceDriver
		upgrade, action, disabled, aggregate, want bool
	}{
		{name: "default-observer", driver: ciskov1.DeviceDriverXE, want: true},
		{name: "upgrade", driver: ciskov1.DeviceDriverXE, upgrade: true, want: true},
		{name: "action", driver: ciskov1.DeviceDriverXE, action: true, want: true},
		{name: "gnoi-disabled-observer", driver: ciskov1.DeviceDriverXE, upgrade: true, disabled: true, want: true},
		{name: "aggregated", driver: ciskov1.DeviceDriverXE, upgrade: true, aggregate: true},
		{name: "nxos", driver: ciskov1.DeviceDriverNXOS, upgrade: true},
		{name: "xr", driver: ciskov1.DeviceDriverXR, action: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(gNOIDisabledEnv, "false")
			t.Setenv("DISABLE_IN_POD_CONFIG_RECONCILER", "false")
			if tc.disabled {
				t.Setenv(gNOIDisabledEnv, "true")
			}
			if tc.aggregate {
				t.Setenv("DISABLE_IN_POD_CONFIG_RECONCILER", "true")
			}
			if got := maintenanceEnabled(configReconcilerOptions{Spec: &ciskov1.DeviceSpec{Driver: tc.driver}, EnableIOSXESoftwareUpgrade: tc.upgrade, EnableWriteClassGNOI: tc.action}); got != tc.want {
				t.Fatalf("enabled=%v want %v", got, tc.want)
			}
		})
	}
}

func TestNewMaintenanceCoordinatorUsesDistinctRuntimeIdentity(t *testing.T) {
	t.Setenv("DISABLE_IN_POD_CONFIG_RECONCILER", "false")
	t.Setenv("CONFIG_LEASE_NAMESPACE", "fleet-leases")
	identity := workerRuntimeIdentity{
		DeviceNamespace: "edge",
		DeviceName:      "switch-01",
		DeviceUID:       "22c81400-85ea-4ca8-91ee-07a7c7bd531c",
		NodeName:        "cvk-edge-node-01",
		ManagedTopology: true,
		WorkerRevision:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	coordinator, err := newMaintenanceCoordinator(
		&rest.Config{Host: "https://127.0.0.1"},
		identity,
		configReconcilerOptions{Spec: &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE}},
	)
	if err != nil {
		t.Fatalf("newMaintenanceCoordinator() error = %v", err)
	}
	if coordinator == nil {
		t.Fatal("newMaintenanceCoordinator() = nil")
	}
	if coordinator.Namespace != identity.DeviceNamespace || coordinator.DeviceName != identity.DeviceName || coordinator.NodeName != identity.NodeName {
		t.Fatalf("coordinator identity = namespace=%q device=%q node=%q", coordinator.Namespace, coordinator.DeviceName, coordinator.NodeName)
	}
	if coordinator.LeaseNamespace != "fleet-leases" {
		t.Fatalf("LeaseNamespace = %q", coordinator.LeaseNamespace)
	}

	t.Setenv("CONFIG_LEASE_NAMESPACE", "")
	coordinator, err = newMaintenanceCoordinator(
		&rest.Config{Host: "https://127.0.0.1"},
		identity,
		configReconcilerOptions{Spec: &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE}},
	)
	if err != nil {
		t.Fatalf("newMaintenanceCoordinator() with default lease namespace error = %v", err)
	}
	if coordinator.LeaseNamespace != identity.DeviceNamespace {
		t.Fatalf("default LeaseNamespace = %q, want %q", coordinator.LeaseNamespace, identity.DeviceNamespace)
	}
}

func TestNewMaintenanceCoordinatorManagedRequiresDeviceUID(t *testing.T) {
	t.Setenv("DISABLE_IN_POD_CONFIG_RECONCILER", "false")
	identity := workerRuntimeIdentity{
		DeviceNamespace: "edge",
		DeviceName:      "switch-01",
		NodeName:        "cvk-edge-node-01",
		ManagedTopology: true,
		WorkerRevision:  "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}

	_, err := newMaintenanceCoordinator(
		&rest.Config{Host: "https://127.0.0.1"},
		identity,
		configReconcilerOptions{Spec: &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXE}},
	)
	if err == nil || !strings.Contains(err.Error(), envDeviceUID) {
		t.Fatalf("error = %v, want missing %s", err, envDeviceUID)
	}
}

func TestNewMaintenanceCoordinatorManagedProtectsNonIOSXEAppWrites(t *testing.T) {
	t.Setenv("DISABLE_IN_POD_CONFIG_RECONCILER", "false")
	identity := workerRuntimeIdentity{
		DeviceNamespace: "edge", DeviceName: "router-01", DeviceUID: "device-uid",
		NodeName: "cvk-router-01", ManagedTopology: true,
		WorkerRevision: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	coordinator, err := newMaintenanceCoordinator(
		&rest.Config{Host: "https://127.0.0.1"}, identity,
		configReconcilerOptions{Spec: &ciskov1.DeviceSpec{Driver: ciskov1.DeviceDriverXR}},
	)
	if err != nil {
		t.Fatalf("newMaintenanceCoordinator() error = %v", err)
	}
	if coordinator == nil || !coordinator.ManagedTopology || coordinator.MutationsEnabled {
		t.Fatalf("managed non-IOS-XE write fence = %#v", coordinator)
	}
}
