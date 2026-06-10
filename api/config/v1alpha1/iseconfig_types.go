// Copyright (c) 2026 Cisco Systems Inc.
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

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ISEConfigSpec declares the desired ISE configuration for a single CiscoDevice.
// The source payload follows the Network as Code ISE model: either a full
// `ise:` envelope or the inner configuration block can be supplied.
type ISEConfigSpec struct {
	// DeviceRef targets the CiscoDevice this configuration applies to.
	// +kubebuilder:validation:Required
	DeviceRef DeviceRef `json:"deviceRef"`

	// ManagedFamilies is the closed list of NetAsCode ISE top-level families
	// this CR owns. Supported families are identity_management,
	// network_resources, network_access, device_administration, trust_sec, and
	// system.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	ManagedFamilies []string `json:"managedFamilies"`

	// Source carries the per-device NetAsCode ISE configuration payload.
	// +kubebuilder:validation:Required
	Source ConfigurationSource `json:"source"`

	// ModelSource records the external Network as Code model/export that
	// produced Source. It is deliberately metadata-only: CVK reconciles the
	// canonical payload in Source.
	// +optional
	ModelSource *NetAsCodeModelSource `json:"modelSource,omitempty"`

	// DriftDetectInterval is the cadence at which the driver re-fetches and
	// compares observed state to intent when the spec is otherwise quiescent.
	// Parsed as a Go duration; minimum 30s to avoid device hammering.
	// +kubebuilder:default="5m"
	// +optional
	DriftDetectInterval string `json:"driftDetectInterval,omitempty"`

	// DriftPolicy controls what happens when drift is found.
	// +kubebuilder:default=revert
	// +optional
	DriftPolicy DriftPolicy `json:"driftPolicy,omitempty"`

	// SecretRefs lets the resolver merge sensitive configuration into the
	// resolved ISE intent from Kubernetes Secrets, so shared secrets and user
	// passwords never need to live in a ConfigMap or git-tracked YAML.
	// +optional
	SecretRefs []FamilySecretRef `json:"secretRefs,omitempty"`
}

// ISEConfigStatus reports reconciliation state to users and GitOps agents.
type ISEConfigStatus struct {
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

	// LastDeviceCheck records the most recent reconcile tick that reached ISE.
	// +optional
	LastDeviceCheck *metav1.Time `json:"lastDeviceCheck,omitempty"`

	// FamilyStatus reports per-family state for each family in ManagedFamilies.
	// +optional
	// +listType=map
	// +listMapKey=name
	FamilyStatus []FamilyStatus `json:"familyStatus,omitempty"`

	// Drift lists the currently known divergences between intent and device.
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

// ISEConfig is the per-device desired-configuration CR for Cisco ISE.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=isecfg
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Drift",type=string,JSONPath=`.spec.driftPolicy`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type ISEConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ISEConfigSpec   `json:"spec"`
	Status ISEConfigStatus `json:"status,omitempty"`
}

// ISEConfigList is the list type for ISEConfig.
//
// +kubebuilder:object:root=true
type ISEConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ISEConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ISEConfig{}, &ISEConfigList{})
}
