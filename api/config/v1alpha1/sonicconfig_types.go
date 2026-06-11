// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// SONICConfigSpec declares OpenConfig desired state for one SONiC CiscoDevice.
// The source payload accepts either an `openconfig:` envelope or the inner
// operation set directly. Supported operation keys are replace, update, and
// delete. replace/update may be lists of {path,value} objects or a map of
// path-to-value; delete may be a list of paths or {path} objects.
type SONICConfigSpec struct {
	// DeviceRef targets the CiscoDevice this configuration applies to.
	// +kubebuilder:validation:Required
	DeviceRef DeviceRef `json:"deviceRef"`

	// ManagedPaths are the OpenConfig path prefixes this CR owns. Every operation
	// path must be equal to or below one of these prefixes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	ManagedPaths []string `json:"managedPaths"`

	// Source carries the OpenConfig operation payload.
	// +kubebuilder:validation:Required
	Source ConfigurationSource `json:"source"`

	// ModelSource records the external model/export that produced Source. It is
	// metadata only; CVK reconciles the canonical payload in Source.
	// +optional
	ModelSource *NetAsCodeModelSource `json:"modelSource,omitempty"`

	// DriftDetectInterval is the cadence at which the driver re-checks the device
	// when the spec is otherwise quiescent. Parsed as a Go duration; minimum 30s.
	// +kubebuilder:default="5m"
	// +optional
	DriftDetectInterval string `json:"driftDetectInterval,omitempty"`

	// DriftPolicy controls write behaviour. report records planned operations
	// without writing; pause disables reconciliation; revert applies intent.
	// +kubebuilder:default=revert
	// +optional
	DriftPolicy DriftPolicy `json:"driftPolicy,omitempty"`
}

// SONICConfigStatus reports OpenConfig reconciliation state.
type SONICConfigStatus struct {
	// Phase is a coarse state summary: Pending, Validating, Applying, InSync,
	// Drifted, Failed, or Paused.
	// +kubebuilder:validation:Enum=Pending;Validating;Applying;InSync;Drifted;Failed;Paused
	// +optional
	Phase string `json:"phase,omitempty"`

	// ObservedGeneration is the .metadata.generation the driver last acted on.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastAppliedHash is a stable SHA-256 over the canonical resolved intent.
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// LastAppliedTime records the most recent successful apply.
	// +optional
	LastAppliedTime *metav1.Time `json:"lastAppliedTime,omitempty"`

	// LastDeviceCheck records the most recent reconcile tick that reached SONiC.
	// +optional
	LastDeviceCheck *metav1.Time `json:"lastDeviceCheck,omitempty"`

	// FamilyStatus reports per-managed-path state. The common FamilyStatus type
	// is reused, with Name containing the OpenConfig path prefix.
	// +optional
	// +listType=map
	// +listMapKey=name
	FamilyStatus []FamilyStatus `json:"familyStatus,omitempty"`

	// Drift lists planned or observed OpenConfig differences.
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

// SONICConfig is the per-device OpenConfig desired-state CR for Cisco SONiC.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=soniccfg
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Drift",type=string,JSONPath=`.spec.driftPolicy`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type SONICConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SONICConfigSpec   `json:"spec"`
	Status SONICConfigStatus `json:"status,omitempty"`
}

// SONICConfigList is the list type for SONICConfig.
//
// +kubebuilder:object:root=true
type SONICConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SONICConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&SONICConfig{}, &SONICConfigList{})
}
