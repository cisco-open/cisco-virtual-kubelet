// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const TypeName = "catalyst-center"

const (
	CapabilityHealth          = "health"
	CapabilityInventory       = "inventory"
	CapabilitySWIM            = "software"
	CapabilityPnPRegistration = "pnp-registration-stage"
)

// Descriptor declares the controller-centric Catalyst Center Network as Code
// baseline. SWIM runs only through a managed device-worker handoff.
func Descriptor() controlleradapter.Descriptor {
	return controlleradapter.Descriptor{
		Type: TypeName, DisplayName: "Catalyst Center",
		NetAsCode: ciskov1.NetworkControllerNetAsCodeStatus{
			Format: nacmodel.Format, Stripe: nacmodel.Stripe,
			ModelVersions: []string{nacmodel.Version},
			Sections:      nacmodel.Sections(),
		},
		Capabilities:      []string{CapabilityHealth, CapabilityInventory, CapabilitySWIM, CapabilityPnPRegistration, ciskov1.NetworkControllerMetadataHandoffProtocol, "private-input-comparison", "lan-device-verification", "lan-link-apply", "lan-link-delete", "lan-link-compose", "feature-config-deletion", "discovery-config-deletion", "tag-config-deletion"},
		WorkerClusterRole: controlleradapter.CatalystCenterWorkerClusterRole,
	}
}

func init() {
	controlleradapter.Register(controlleradapter.Registration{Descriptor: Descriptor(), Factory: newAdapter, ConfigurationMetadataScope: &controlleradapter.ConfigurationMetadataScope{Selected: preparedPnPRemovalScopeSelected, PhysicalIdentities: preparedPnPRemovalPhysicalIdentities, Settlement: pnpMetadataSettlementState, Cancellation: pnpMetadataCancellationState}, ConfigurationLifecycle: &controlleradapter.ConfigurationLifecycle{Finalizer: featureConfigFinalizer, Eligible: featureDeletionEligible, Completed: featureDeletionCompleted}, ConfigurationLifecycles: []controlleradapter.ConfigurationLifecycle{{Finalizer: discoveryConfigFinalizer, Eligible: discoveryDeletionEligible, Completed: discoveryDeletionCompleted}, {Finalizer: tagConfigFinalizer, Eligible: tagDeletionEligible, Completed: tagDeletionCompleted}}})
}
