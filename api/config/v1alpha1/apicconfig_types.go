// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// APICConfigSpec declares the desired APIC configuration for a single CiscoDevice.
// The source payload follows the Network as Code APIC model: either a full
// `apic:` envelope or the inner APIC configuration block can be supplied. When a
// full model includes top-level `existing.apic`, that read-only reference model
// is preserved under the canonical `existing` family for resolvers.
type APICConfigSpec struct {
	// DeviceRef targets the CiscoDevice this configuration applies to.
	// +kubebuilder:validation:Required
	DeviceRef DeviceRef `json:"deviceRef"`

	// ManagedFamilies is the closed list of NetAsCode APIC top-level families
	// this CR owns. Supported families are bootstrap, fabric_policies,
	// access_policies, pod_policies, node_policies, interface_policies,
	// tenants, and existing.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	ManagedFamilies []string `json:"managedFamilies"`

	// Source carries the per-controller Network as Code APIC configuration payload.
	// +kubebuilder:validation:Required
	Source ConfigurationSource `json:"source"`

	// ModelSource records the external Network as Code model/export that
	// produced Source. It is deliberately metadata-only: CVK reconciles the
	// canonical payload in Source.
	// +optional
	ModelSource *NetAsCodeModelSource `json:"modelSource,omitempty"`

	// DriftDetectInterval is the cadence at which the driver re-fetches and
	// compares observed state to intent when the spec is otherwise quiescent.
	// Parsed as a Go duration; minimum 30s to avoid controller/API hammering.
	// +kubebuilder:default="5m"
	// +optional
	DriftDetectInterval string `json:"driftDetectInterval,omitempty"`

	// DriftPolicy controls what happens when drift is found.
	// +kubebuilder:default=revert
	// +optional
	DriftPolicy DriftPolicy `json:"driftPolicy,omitempty"`

	// SecretRefs lets the resolver merge sensitive configuration into the
	// resolved APIC intent from Kubernetes Secrets, so shared secrets and API
	// credentials never need to live in a ConfigMap or git-tracked YAML.
	// +optional
	SecretRefs []FamilySecretRef `json:"secretRefs,omitempty"`
}

// APICConfigStatus reports reconciliation state to users and GitOps agents.
type APICConfigStatus struct {
	// Phase is a coarse state summary: Pending, Validating, Applying, InSync,
	// Drifted, Failed, or Paused.
	// +kubebuilder:validation:Enum=Pending;Validating;Applying;InSync;Drifted;Failed;Paused
	// +optional
	Phase string `json:"phase,omitempty"`

	// ObservedGeneration is the .metadata.generation the driver last acted on.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastAppliedHash is a stable SHA-256 over the canonicalised resolved intent.
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// LastAppliedTime records the most recent successful apply.
	// +optional
	LastAppliedTime *metav1.Time `json:"lastAppliedTime,omitempty"`

	// LastDeviceCheck records the most recent reconcile tick that reached APIC.
	// +optional
	LastDeviceCheck *metav1.Time `json:"lastDeviceCheck,omitempty"`

	// FamilyStatus reports per-family state for each family in ManagedFamilies.
	// +optional
	// +listType=map
	// +listMapKey=name
	FamilyStatus []FamilyStatus `json:"familyStatus,omitempty"`

	// Drift lists the currently known divergences between intent and controller.
	// +optional
	// +kubebuilder:validation:MaxItems=50
	Drift []DriftEntry `json:"drift,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// APICConfig is the per-controller desired-configuration CR for Cisco APIC.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=apiccfg
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Drift",type=string,JSONPath=`.spec.driftPolicy`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type APICConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   APICConfigSpec   `json:"spec"`
	Status APICConfigStatus `json:"status,omitempty"`
}

// APICConfigList is the list type for APICConfig.
//
// +kubebuilder:object:root=true
type APICConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []APICConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&APICConfig{}, &APICConfigList{})
}
