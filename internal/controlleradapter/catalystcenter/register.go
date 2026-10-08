// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

const TypeName = "catalyst-center"

const (
	CapabilityHealth    = "health"
	CapabilityInventory = "inventory"
	CapabilitySWIM      = "software"
)

// Descriptor declares the controller-centric Catalyst Center Network as Code
// contract. SWIM is exposed as an operational capability; it is not a
// Network as Code section in the upstream model.
func Descriptor() controlleradapter.Descriptor {
	return controlleradapter.Descriptor{
		Type: TypeName, DisplayName: "Catalyst Center",
		NetAsCode: ciskov1.NetworkControllerNetAsCodeStatus{
			Format: "netascode-catalyst-center", Stripe: "catalyst_center",
			ModelVersions: []string{"0.5.0"},
			Sections:      []string{"sites", "network_settings", "network_profiles", "fabric", "templates", "inventory", "wireless", "lan_automation", "system_settings"},
		},
		Capabilities:      []string{CapabilityHealth, CapabilityInventory},
		WorkerClusterRole: controlleradapter.DefaultWorkerClusterRole,
	}
}

func init() {
	controlleradapter.Register(controlleradapter.Registration{Descriptor: Descriptor(), Factory: newAdapter})
}
