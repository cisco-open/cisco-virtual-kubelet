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
	"k8s.io/apimachinery/pkg/runtime"
)

// IOSXEConfigDefaultsSpec carries configuration merged into every resolved
// intent at the lowest precedence. Mirrors netascode's `iosxe.defaults`.
type IOSXEConfigDefaultsSpec struct {
	// Configuration holds the netascode-shaped configuration body applied
	// as a baseline to every device.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Configuration runtime.RawExtension `json:"configuration"`
}

// IOSXEConfigDefaultsStatus reports aggregate application state across the
// devices this defaults object influences. Per-device detail lives on
// individual IOSXEConfig CRs.
type IOSXEConfigDefaultsStatus struct {
	// ObservedGeneration is the .metadata.generation the aggregator last read.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// AffectedDevices counts the CiscoDevices whose resolved intent includes
	// this defaults object.
	// +optional
	AffectedDevices int32 `json:"affectedDevices,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// IOSXEConfigDefaults is the cluster-scoped baseline merged into every
// resolved intent. At most a handful of these are expected in a cluster;
// the driver merges them in name order for deterministic precedence.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=iosxedefaults
// +kubebuilder:printcolumn:name="Affected",type=integer,JSONPath=`.status.affectedDevices`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXEConfigDefaults struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXEConfigDefaultsSpec   `json:"spec"`
	Status IOSXEConfigDefaultsStatus `json:"status,omitempty"`
}

// IOSXEConfigDefaultsList is the list type for IOSXEConfigDefaults.
//
// +kubebuilder:object:root=true
type IOSXEConfigDefaultsList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXEConfigDefaults `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXEConfigDefaults{}, &IOSXEConfigDefaultsList{})
}
