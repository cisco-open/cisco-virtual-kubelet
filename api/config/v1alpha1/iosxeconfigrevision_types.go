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

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// IOSXEConfigRevisionSpec stores one successfully applied resolved intent for
// an IOSXEConfig. It is intentionally immutable from the reconciler's point of
// view: rollback selects a revision and re-runs the normal IOSXEConfig apply
// path rather than invoking a separate rollback engine.
type IOSXEConfigRevisionSpec struct {
	// DeviceRef targets the CiscoDevice the revision was applied to.
	// +kubebuilder:validation:Required
	DeviceRef DeviceRef `json:"deviceRef"`

	// SourceRef is the namespace/name of the IOSXEConfig that produced this
	// revision.
	// +kubebuilder:validation:Required
	SourceRef string `json:"sourceRef"`

	// SourceUID is the UID of the IOSXEConfig that produced this revision.
	// +kubebuilder:validation:Required
	SourceUID string `json:"sourceUID"`

	// SourceGeneration is the IOSXEConfig generation that produced this
	// revision.
	// +optional
	SourceGeneration int64 `json:"sourceGeneration,omitempty"`

	// Hash is the canonical resolved-intent hash that was applied.
	// +kubebuilder:validation:Required
	Hash string `json:"hash"`

	// Body is a versioned JSON replay body containing the resolved
	// configuration and CLI blocks. It uses the same shape as
	// IOSXEConfigApplyLog entries so rollback and replay share one decoder.
	// +kubebuilder:validation:Required
	Body string `json:"body"`

	// AtomicReplaceOwnedKeys snapshots the CR-owned keys after the apply.
	// +optional
	AtomicReplaceOwnedKeys map[string][]string `json:"atomicReplaceOwnedKeys,omitempty"`
}

// IOSXEConfigRevisionStatus carries lightweight lifecycle information for
// operators and GC.
type IOSXEConfigRevisionStatus struct {
	// CreatedAt records when the reconciler created the revision.
	// +optional
	CreatedAt *metav1.Time `json:"createdAt,omitempty"`
}

// IOSXEConfigRevision stores durable resolved intent history for
// IOSXEConfig rollback.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=iosxerev
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Source",type=string,JSONPath=`.spec.sourceRef`
// +kubebuilder:printcolumn:name="Generation",type=integer,JSONPath=`.spec.sourceGeneration`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXEConfigRevision struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXEConfigRevisionSpec   `json:"spec"`
	Status IOSXEConfigRevisionStatus `json:"status,omitempty"`
}

// IOSXEConfigRevisionList is the list type for IOSXEConfigRevision.
//
// +kubebuilder:object:root=true
type IOSXEConfigRevisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXEConfigRevision `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXEConfigRevision{}, &IOSXEConfigRevisionList{})
}
