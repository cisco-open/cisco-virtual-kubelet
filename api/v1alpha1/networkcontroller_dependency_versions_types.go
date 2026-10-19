// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package v1alpha1

// NetworkControllerConfigurationDependencyVersions pins ordered nonsecret
// native version identities for restart recovery. It contains no content,
// parameters, credentials or content hashes. The first entry is the parent;
// subsequent entries follow its native dependency order. Runtime admission of
// this envelope is separate from schema preservation.
type NetworkControllerConfigurationDependencyVersions struct {
	// +kubebuilder:validation:Enum=template-graph-v1
	Kind string `json:"kind"`
	// +kubebuilder:validation:MinItems=2
	// +kubebuilder:validation:MaxItems=1001
	// +listType=atomic
	Entries []NetworkControllerConfigurationDependencyVersion `json:"entries"`
}

// NetworkControllerConfigurationDependencyVersion identifies one committed
// native object and version. Display names and template values are excluded.
type NetworkControllerConfigurationDependencyVersion struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-]+$`
	ID string `json:"id"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-]+$`
	VersionID string `json:"versionID"`
	// +kubebuilder:validation:Minimum=1
	Version int64 `json:"version"`
}
