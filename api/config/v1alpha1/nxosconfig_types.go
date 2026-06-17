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

// NXOSConfigSpec declares desired NX-OS configuration for a single
// CiscoDevice. The source payload follows the Network as Code NX-OS model:
// either a full `nxos:` envelope or the inner per-device configuration block
// can be supplied. The first supported families are system, vlan, and
// interface_ethernet.
type NXOSConfigSpec CommonConfigSpec

// NXOSConfigStatus reports reconciliation state to users and GitOps agents.
type NXOSConfigStatus CommonConfigStatus

// NXOSConfig is the per-device desired-configuration CR for Cisco NX-OS.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=nxoscfg
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Drift",type=string,JSONPath=`.spec.driftPolicy`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type NXOSConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NXOSConfigSpec   `json:"spec"`
	Status NXOSConfigStatus `json:"status,omitempty"`
}

// NXOSConfigList is the list type for NXOSConfig.
//
// +kubebuilder:object:root=true
type NXOSConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NXOSConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&NXOSConfig{}, &NXOSConfigList{})
}
