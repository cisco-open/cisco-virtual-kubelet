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

// Package nexusdashboard is the Cisco Nexus Dashboard (ND) controller adapter.
// It targets the ND 4.x /api/v1 Infra and Manage APIs, not the NDFC legacy
// /appcenter APIs. This package is private to the cisco-vk composition root.
package nexusdashboard

import (
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// TypeName is the NetworkController.spec.type handled by this adapter.
const TypeName = "nexus-dashboard"

// CapabilityHealth is reported once login and the Manage API probe succeed.
const CapabilityHealth = "health"

// Descriptor returns the adapter's registry descriptor. The nd Network as Code
// section is declared so the neutral contract accepts the stripe, but no intent
// reconciliation is implemented yet.
func Descriptor() controlleradapter.Descriptor {
	return controlleradapter.Descriptor{
		Type:        TypeName,
		DisplayName: "Nexus Dashboard",
		NetAsCode: ciskov1.NetworkControllerNetAsCodeStatus{
			Format:        "netascode-nexus-dashboard",
			Stripe:        "nd",
			ModelVersions: []string{"nd-4.2"},
			Sections:      []string{"nd"},
		},
		Capabilities:      []string{CapabilityHealth, CapabilityInventory},
		WorkerClusterRole: controlleradapter.DefaultWorkerClusterRole,
	}
}

func init() {
	controlleradapter.Register(controlleradapter.Registration{
		Descriptor: Descriptor(),
		Factory:    newAdapter,
	})
}
