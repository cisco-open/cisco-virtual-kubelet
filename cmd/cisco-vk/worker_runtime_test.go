// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
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

	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
)

func TestResolveWorkerRuntimeProfile(t *testing.T) {
	for _, name := range []string{managedprotocol.EnvWorkerMode, managedprotocol.EnvWorkerAccess} {
		t.Setenv(name, "")
	}

	t.Run("standalone defaults preserve combined read-write behavior", func(t *testing.T) {
		got, err := resolveWorkerRuntimeProfile("", "", false)
		if err != nil {
			t.Fatalf("resolveWorkerRuntimeProfile() error = %v", err)
		}
		if got.Mode != workerModeCombined || got.Access != workerAccessReadWrite {
			t.Fatalf("profile = %+v, want combined/readWrite", got)
		}
		if !got.runsAppHosting() || !got.runsNetworkManagement() {
			t.Fatalf("combined profile capabilities = app:%t network:%t, want both", got.runsAppHosting(), got.runsNetworkManagement())
		}
	})

	t.Run("environment configures managed network reader", func(t *testing.T) {
		t.Setenv(managedprotocol.EnvWorkerMode, string(workerModeNetworkManagement))
		t.Setenv(managedprotocol.EnvWorkerAccess, string(workerAccessReadOnly))
		got, err := resolveWorkerRuntimeProfile("", "", true)
		if err != nil {
			t.Fatalf("resolveWorkerRuntimeProfile() error = %v", err)
		}
		if got.Mode != workerModeNetworkManagement || got.Access != workerAccessReadOnly {
			t.Fatalf("profile = %+v, want network-management/readOnly", got)
		}
		if got.runsAppHosting() || !got.runsNetworkManagement() {
			t.Fatalf("network profile capabilities = app:%t network:%t", got.runsAppHosting(), got.runsNetworkManagement())
		}
	})

	t.Run("flags override environment", func(t *testing.T) {
		t.Setenv(managedprotocol.EnvWorkerMode, string(workerModeNetworkManagement))
		t.Setenv(managedprotocol.EnvWorkerAccess, string(workerAccessReadOnly))
		got, err := resolveWorkerRuntimeProfile(string(workerModeAppHosting), string(workerAccessReadWrite), true)
		if err != nil {
			t.Fatalf("resolveWorkerRuntimeProfile() error = %v", err)
		}
		if got.Mode != workerModeAppHosting || got.Access != workerAccessReadWrite {
			t.Fatalf("profile = %+v, want app-hosting/readWrite", got)
		}
	})
}

func TestWorkerRuntimeProfileRejectsUnsafeCombinations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    workerMode
		access  workerAccess
		managed bool
		want    string
	}{
		{name: "unknown mode", mode: "all", access: workerAccessReadWrite, want: "invalid worker mode"},
		{name: "unknown access", mode: workerModeNetworkManagement, access: "reader", want: "invalid worker access"},
		{name: "combined managed", mode: workerModeCombined, access: workerAccessReadWrite, managed: true, want: "standalone compatibility mode"},
		{name: "app reader", mode: workerModeAppHosting, access: workerAccessReadOnly, managed: true, want: "can receive Pod create, update, and delete"},
		{name: "combined reader", mode: workerModeCombined, access: workerAccessReadOnly, want: "can receive Pod create, update, and delete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := (workerRuntimeProfile{Mode: tc.mode, Access: tc.access}).validate(tc.managed)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("validate() error = %v, want text %q", err, tc.want)
			}
		})
	}
}

func TestReadOnlyNetworkProfileDisablesEveryMutationGate(t *testing.T) {
	profile := workerRuntimeProfile{Mode: workerModeNetworkManagement, Access: workerAccessReadOnly}
	got := profile.constrainNetworkOptions(configReconcilerOptions{
		EnableWriteClassGNOI:       true,
		EnableIOSXESoftwareUpgrade: true,
	})
	if !got.ReadOnly {
		t.Fatal("ReadOnly = false, want true")
	}
	if got.EnableWriteClassGNOI || got.EnableIOSXESoftwareUpgrade {
		t.Fatalf("read-only mutation gates = action:%t upgrade:%t, want both false",
			got.EnableWriteClassGNOI, got.EnableIOSXESoftwareUpgrade)
	}
	if got.configWritesEnabled() || got.softwareUpgradeEnabled() || got.writeClassGNOIEnabled() {
		t.Fatalf("read-only effective mutations = config:%t upgrade:%t action:%t, want all false",
			got.configWritesEnabled(), got.softwareUpgradeEnabled(), got.writeClassGNOIEnabled())
	}
}

func TestReadWriteNetworkProfilePreservesExplicitMutationGates(t *testing.T) {
	profile := workerRuntimeProfile{Mode: workerModeNetworkManagement, Access: workerAccessReadWrite}
	got := profile.constrainNetworkOptions(configReconcilerOptions{
		EnableWriteClassGNOI:       true,
		EnableIOSXESoftwareUpgrade: true,
	})
	if got.ReadOnly || !got.EnableWriteClassGNOI || !got.EnableIOSXESoftwareUpgrade {
		t.Fatalf("read-write options = %+v, want both explicit mutation gates preserved", got)
	}
	if !got.configWritesEnabled() || !got.softwareUpgradeEnabled() || !got.writeClassGNOIEnabled() {
		t.Fatalf("read-write effective mutations = config:%t upgrade:%t action:%t, want all true",
			got.configWritesEnabled(), got.softwareUpgradeEnabled(), got.writeClassGNOIEnabled())
	}
}
