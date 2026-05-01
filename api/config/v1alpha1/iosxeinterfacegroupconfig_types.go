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

// InterfaceMatch identifies the set of interfaces on a CiscoDevice an
// InterfaceGroupConfig applies to. Mirror of netascode's interface-group
// membership rules: either an explicit list of (type, name) pairs, a
// type filter that applies to every interface of that YANG subtype, or
// a regex pattern that selects every matching interface name within
// the resolved intent.
//
// At most one of Name and NamePattern may be set.
//
// +kubebuilder:validation:XValidation:rule="!(has(self.name) && has(self.namePattern))",message="at most one of name and namePattern may be set"
type InterfaceMatch struct {
	// Type is the YANG interface subtree name (GigabitEthernet,
	// TenGigabitEthernet, Loopback, VirtualPortGroup, Vlan,
	// Port-channel, Tunnel). Required.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Type string `json:"type"`

	// Name is the interface instance name ("0/0/0", "0", "10"). When
	// empty (and NamePattern is also empty), the group applies to every
	// interface of the given Type on every matched device.
	// +optional
	Name string `json:"name,omitempty"`

	// NamePattern is a Go-syntax regular expression matched against
	// the `name` field of every interface entry of the matching Type
	// already declared in the resolved intent (defaults + device groups
	// + templates + per-device source). The group's Configuration is
	// projected onto each matched entry. The pattern is anchored —
	// callers do not need to write ^…$ explicitly.
	//
	// Mutually exclusive with Name. When neither is set the group
	// applies to every interface of the given Type that resolution
	// surfaces.
	//
	// Pattern expansion only matches interfaces already in the resolved
	// intent; it does not query the device. Operators who need to
	// apply policy to interfaces that aren't otherwise declared should
	// add explicit entries (commonly via a defaults block) so the
	// pattern has something to match.
	// +optional
	NamePattern string `json:"namePattern,omitempty"`
}

// IOSXEInterfaceGroupConfigSpec carries a configuration block that
// applies to a set of interfaces across a set of devices. The merge
// layer expands each member interface into the resolved intent's
// interface-family block (interface_ethernet, interface_loopback,
// interface_vlan, etc. — chosen by InterfaceMatch.Type) as if the
// operator had written the same entry in a per-device source.
//
// Mirrors netascode's `interface_groups` scope. Order of precedence
// slots between IOSXEDeviceGroupConfig and IOSXETemplate:
//
//	defaults → device groups → interface groups → templates → per-device
//
// Scope precedence matches netascode; rightmost wins on overlap.
type IOSXEInterfaceGroupConfigSpec struct {
	// DeviceRefs explicitly lists the CiscoDevices whose interfaces are
	// candidates for group membership.
	// +optional
	DeviceRefs []DeviceRef `json:"deviceRefs,omitempty"`

	// DeviceSelector matches CiscoDevices by their metadata labels.
	// A device matched by DeviceSelector but whose interfaces do not
	// match InterfaceSelector is silently excluded; both filters must
	// pass for an interface to be included.
	// +optional
	DeviceSelector *metav1.LabelSelector `json:"deviceSelector,omitempty"`

	// InterfaceSelector selects which interfaces on the matched
	// devices participate. At least one Match must be supplied.
	// +kubebuilder:validation:MinItems=1
	InterfaceSelector []InterfaceMatch `json:"interfaceSelector"`

	// Configuration is the netascode-shaped body that is merged into
	// the resolved intent for every matched (device, interface) pair.
	// Shape must be a single interface-family block (e.g.
	// {"interface_ethernet": {"interfaces": [{...}]}}); the resolver
	// projects InterfaceMatch (type, name) into each entry so the
	// operator need not repeat them.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Configuration runtime.RawExtension `json:"configuration"`
}

// IOSXEInterfaceGroupConfigStatus reports aggregate resolution state.
type IOSXEInterfaceGroupConfigStatus struct {
	// ObservedGeneration is the .metadata.generation the aggregator last read.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// MemberDevices is the number of CiscoDevices whose interfaces
	// this group currently resolves onto.
	// +optional
	MemberDevices int32 `json:"memberDevices,omitempty"`

	// MemberInterfaces is the total (device, interface) pair count
	// resolved across every member device.
	// +optional
	MemberInterfaces int32 `json:"memberInterfaces,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// IOSXEInterfaceGroupConfig is the shared-configuration object for a
// set of interfaces across a set of devices. Mirrors netascode's
// `interface_groups[]`.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=iosxeifgroup
// +kubebuilder:printcolumn:name="Members",type=integer,JSONPath=`.status.memberInterfaces`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXEInterfaceGroupConfig struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXEInterfaceGroupConfigSpec   `json:"spec"`
	Status IOSXEInterfaceGroupConfigStatus `json:"status,omitempty"`
}

// IOSXEInterfaceGroupConfigList is the list type for
// IOSXEInterfaceGroupConfig.
//
// +kubebuilder:object:root=true
type IOSXEInterfaceGroupConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXEInterfaceGroupConfig `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXEInterfaceGroupConfig{}, &IOSXEInterfaceGroupConfigList{})
}
