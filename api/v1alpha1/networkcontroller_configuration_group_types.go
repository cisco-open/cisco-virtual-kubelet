// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package v1alpha1

import ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"

// NetworkControllerConfigurationGroupIdentity persists the full physical target
// set with its operation. It is evidence, not a reservation or dispatch grant.
// Runtime writers must gate schema and worker compatibility before publishing it.
type NetworkControllerConfigurationGroupIdentity struct {
	// +kubebuilder:validation:Enum=controller-configuration-group-identity-v1
	Version string `json:"version"`
	// Digest binds the operation's immutable fields and complete target set.
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	Digest string `json:"digest"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1000
	// +listType=map
	// +listMapKey=physicalIdentity
	Targets []NetworkControllerConfigurationGroupTarget `json:"targets"`
}

type NetworkControllerConfigurationGroupTarget struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	InventoryID string `json:"inventoryID"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	PhysicalIdentity string                                `json:"physicalIdentity"`
	Device           ops.ConfigurationReservationReference `json:"device"`
	Node             ops.ConfigurationReservationReference `json:"node"`
}
