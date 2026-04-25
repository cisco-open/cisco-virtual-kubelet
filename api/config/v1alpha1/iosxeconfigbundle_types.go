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

// IOSXEConfigBundleSpec describes a configuration that should be
// fanned out across a set of CiscoDevices. The bundle controller
// creates one IOSXEConfig CR per matched device, owned by the
// bundle so that deleting the bundle garbage-collects every child.
//
// netascode-style "configure every device with role=spine" lands
// here: the operator authors the per-device spec once and the
// controller materialises N IOSXEConfig CRs.
type IOSXEConfigBundleSpec struct {
	// DeviceRefs explicitly names CiscoDevices to fan out to. Either
	// DeviceRefs or DeviceSelector must produce at least one device;
	// an empty match set is reported on Status.Conditions[Ready] but
	// is not an error condition (the operator may be authoring
	// against a not-yet-deployed fleet).
	// +optional
	DeviceRefs []DeviceRef `json:"deviceRefs,omitempty"`

	// DeviceSelector matches CiscoDevices by their metadata labels.
	// The intersection-of-conditions semantics match Kubernetes'
	// LabelSelector contract.
	// +optional
	DeviceSelector *metav1.LabelSelector `json:"deviceSelector,omitempty"`

	// Template is the per-device spec the controller stamps onto
	// every generated child IOSXEConfig. The controller fills
	// DeviceRef per device during fan-out, so the template type
	// here deliberately omits DeviceRef — operators don't write
	// a dummy value just to clear admission. Wave 3B
	// (external-review Finding #10).
	// +kubebuilder:validation:Required
	Template IOSXEConfigTemplateSpec `json:"template"`
}

// IOSXEConfigBundleStatus reports the controller's view of the
// fanout: which children exist, which devices they target, and
// whether everything resolved.
type IOSXEConfigBundleStatus struct {
	// ObservedGeneration is the .metadata.generation the controller
	// last reconciled.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// MemberDevices is the count of CiscoDevices currently in scope.
	// +optional
	MemberDevices int32 `json:"memberDevices,omitempty"`

	// GeneratedCRs lists every child IOSXEConfig the bundle owns,
	// in name-sorted order so changes are diff-friendly.
	// +optional
	GeneratedCRs []GeneratedCR `json:"generatedCRs,omitempty"`

	// Conditions follows the standard Kubernetes shape.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// GeneratedCR pins a child IOSXEConfig to its device and tracks
// the apply phase so an operator can read fleet-wide state from
// the bundle status alone.
type GeneratedCR struct {
	// Name of the child IOSXEConfig (same namespace as the bundle).
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Device the child targets.
	// +kubebuilder:validation:Required
	Device string `json:"device"`

	// Phase is a copy of the child's status.phase, for fleet-wide
	// rollups. Empty when the child is freshly created and hasn't
	// yet run a reconcile.
	// +optional
	Phase string `json:"phase,omitempty"`
}

// IOSXEConfigBundle stamps a per-device IOSXEConfig spec across a
// set of devices.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=iosxebundle
// +kubebuilder:printcolumn:name="Devices",type=integer,JSONPath=`.status.memberDevices`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXEConfigBundle struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXEConfigBundleSpec   `json:"spec"`
	Status IOSXEConfigBundleStatus `json:"status,omitempty"`
}

// IOSXEConfigBundleList is the list type.
//
// +kubebuilder:object:root=true
type IOSXEConfigBundleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXEConfigBundle `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXEConfigBundle{}, &IOSXEConfigBundleList{})
}
