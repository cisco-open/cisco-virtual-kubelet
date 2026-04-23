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

// IOSXEDeviceGroupConfigSpec carries configuration shared across a set of
// CiscoDevices. A device is included in the group when either:
//   - its CiscoDevice name is listed in DeviceRefs, or
//   - its CiscoDevice labels satisfy DeviceSelector.
//
// At least one of the two must be set; setting both is allowed and treated
// as a union.
type IOSXEDeviceGroupConfigSpec struct {
	// DeviceRefs explicitly lists the CiscoDevices that belong to this group.
	// +optional
	DeviceRefs []DeviceRef `json:"deviceRefs,omitempty"`

	// DeviceSelector matches CiscoDevices by their metadata labels.
	// +optional
	DeviceSelector *metav1.LabelSelector `json:"deviceSelector,omitempty"`

	// Configuration holds the netascode-shaped configuration body shared
	// across every member of the group.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Configuration runtime.RawExtension `json:"configuration"`
}

// IOSXEDeviceGroupConfigStatus reports group membership and aggregate state.
type IOSXEDeviceGroupConfigStatus struct {
	// ObservedGeneration is the .metadata.generation the aggregator last read.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// MemberDevices counts the CiscoDevices currently resolved into this group.
	// +optional
	MemberDevices int32 `json:"memberDevices,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// IOSXEDeviceGroupConfig is the namespaced shared-configuration object for a
// set of devices, mirroring netascode's `iosxe.device_groups[]`.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=iosxegroup
// +kubebuilder:printcolumn:name="Members",type=integer,JSONPath=`.status.memberDevices`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXEDeviceGroupConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXEDeviceGroupConfigSpec   `json:"spec"`
	Status IOSXEDeviceGroupConfigStatus `json:"status,omitempty"`
}

// IOSXEDeviceGroupConfigList is the list type for IOSXEDeviceGroupConfig.
//
// +kubebuilder:object:root=true
type IOSXEDeviceGroupConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXEDeviceGroupConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXEDeviceGroupConfig{}, &IOSXEDeviceGroupConfigList{})
}
