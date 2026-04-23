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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// IOSXEConfigSpec declares the desired IOS-XE configuration for a single
// device. The config driver running inside cisco-vk run merges this spec
// with any matching defaults, device groups, and referenced templates to
// produce the resolved intent that is reconciled against the device.
type IOSXEConfigSpec struct {
	// DeviceRef targets the CiscoDevice this configuration applies to.
	// +kubebuilder:validation:Required
	DeviceRef DeviceRef `json:"deviceRef"`

	// DeviceGroups names the IOSXEDeviceGroupConfig CRs whose configuration
	// is merged into the resolved intent before this CR's source.
	// Merge order follows netascode semantics: defaults → groups → templates
	// → per-device, rightmost wins.
	// +optional
	DeviceGroups []string `json:"deviceGroups,omitempty"`

	// TemplateRefs names IOSXETemplate CRs to expand and merge before the
	// per-device source.
	// +optional
	TemplateRefs []TemplateRef `json:"templateRefs,omitempty"`

	// ManagedFamilies is the closed list of netascode families this CR owns.
	// A family outside this list is not written by this CR even when the
	// merged intent contains values for it, so config can be adopted family
	// by family during a cutover.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=set
	ManagedFamilies []string `json:"managedFamilies"`

	// Source carries the per-device netascode configuration payload.
	// +kubebuilder:validation:Required
	Source ConfigurationSource `json:"source"`

	// Transactional requests a candidate-datastore + commit apply when the
	// transport supports it (NETCONF). Ignored with a warning on RESTCONF,
	// which has no candidate datastore; Phase-2 lifts this limitation.
	// +kubebuilder:default=false
	// +optional
	Transactional bool `json:"transactional,omitempty"`

	// WriteStartup copies running-config to startup-config after a successful
	// apply. Off by default to keep reconciles cheap; turn on for devices
	// expected to reboot without a config-save orchestrator.
	// +kubebuilder:default=false
	// +optional
	WriteStartup bool `json:"writeStartup,omitempty"`

	// DriftDetectInterval is the cadence at which the driver re-fetches and
	// compares observed state to intent when the spec is otherwise quiescent.
	// Parsed as a Go duration; minimum 30s to avoid device hammering.
	// +kubebuilder:default="5m"
	// +optional
	DriftDetectInterval string `json:"driftDetectInterval,omitempty"`

	// DriftPolicy controls what happens when drift is found. See DriftPolicy.
	// +kubebuilder:default=revert
	// +optional
	DriftPolicy DriftPolicy `json:"driftPolicy,omitempty"`

	// PruneOnRelinquish, when true, causes families removed from
	// ManagedFamilies to be explicitly deleted from the device on the next
	// reconcile. Default is to leave previously-written configuration in
	// place, matching operator intuition during migrations.
	// +kubebuilder:default=false
	// +optional
	PruneOnRelinquish bool `json:"pruneOnRelinquish,omitempty"`
}

// IOSXEConfigStatus reports reconciliation state to users and GitOps agents.
type IOSXEConfigStatus struct {
	// Phase is a coarse state summary: Pending, Validating, Planning,
	// Applying, Verifying, InSync, Drifted, Failed, or Paused.
	// +kubebuilder:validation:Enum=Pending;Validating;Planning;Applying;Verifying;InSync;Drifted;Failed;Paused
	// +optional
	Phase string `json:"phase,omitempty"`

	// ObservedGeneration is the .metadata.generation the driver last acted on.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastAppliedHash is a stable SHA-256 over the canonicalised resolved
	// intent. The driver short-circuits reconcile when this matches the
	// current intent and the device is known-fresh.
	// +optional
	LastAppliedHash string `json:"lastAppliedHash,omitempty"`

	// LastAppliedTime records the most recent successful apply.
	// +optional
	LastAppliedTime *metav1.Time `json:"lastAppliedTime,omitempty"`

	// SourceYangVersion is the YANG release the driver used to translate
	// the intent on the last successful apply.
	// +optional
	SourceYangVersion string `json:"sourceYangVersion,omitempty"`

	// FamilyStatus reports per-family state for each family in ManagedFamilies.
	// +optional
	// +listType=map
	// +listMapKey=name
	FamilyStatus []FamilyStatus `json:"familyStatus,omitempty"`

	// Drift lists the currently known divergences between intent and device.
	// Capped at 50 entries; additional drift is reflected in counters only.
	// +optional
	Drift []DriftEntry `json:"drift,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape. The
	// driver maintains Ready, Reconciling, and a Healthy-<family> entry
	// for each managed family.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// IOSXEConfig is the per-device desired-configuration CR. A single device
// may be targeted by at most one IOSXEConfig for any given family; the
// driver arbitrates via a per-family lease and surfaces conflicts in status.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=iosxecfg
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Drift",type=string,JSONPath=`.spec.driftPolicy`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXEConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXEConfigSpec   `json:"spec"`
	Status IOSXEConfigStatus `json:"status,omitempty"`
}

// IOSXEConfigList is the list type for IOSXEConfig.
//
// +kubebuilder:object:root=true
type IOSXEConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXEConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXEConfig{}, &IOSXEConfigList{})
}
