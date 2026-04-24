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

// TemplateParameterType restricts template parameters to the small set the
// expander can safely substitute into netascode YAML without introducing
// type confusion at the device.
//
// +kubebuilder:validation:Enum=string;int;bool;ipv4;ipv6;cidr
type TemplateParameterType string

// Supported template parameter types.
const (
	TemplateParameterString TemplateParameterType = "string"
	TemplateParameterInt    TemplateParameterType = "int"
	TemplateParameterBool   TemplateParameterType = "bool"
	TemplateParameterIPv4   TemplateParameterType = "ipv4"
	TemplateParameterIPv6   TemplateParameterType = "ipv6"
	TemplateParameterCIDR   TemplateParameterType = "cidr"
)

// TemplateParameter declares one input to an IOSXETemplate. Expansion
// fails (rather than silently substituting an unparseable value) when
// a required parameter is missing or a supplied value fails the type check.
type TemplateParameter struct {
	// Name of the parameter, referenced from the template body as {{ .Name }}.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Type is the required value shape. Validation is performed at expansion
	// time against the rendered value; invalid inputs fail the expansion
	// rather than reaching the device.
	// +kubebuilder:validation:Required
	Type TemplateParameterType `json:"type"`

	// Required marks the parameter as mandatory; an IOSXEConfig that
	// references this template without supplying a Required parameter fails
	// validation.
	// +kubebuilder:default=false
	// +optional
	Required bool `json:"required,omitempty"`

	// Default is the fallback value when the referring CR omits this
	// parameter. Stored as a string; parsed according to Type at expansion.
	// +optional
	Default string `json:"default,omitempty"`

	// Description is surfaced in tooling (kubectl explain, lint output).
	// +optional
	Description string `json:"description,omitempty"`
}

// TemplateKind distinguishes the two netascode template styles:
//
//   - DataModelTemplate ("data-model"): the template body is a
//     parameterised netascode YAML fragment; expansion substitutes
//     parameters and the result is deep-merged into the resolved
//     intent alongside other scope objects. This is the shape the
//     Phase-1 resolver handles and matches the structured-YAML
//     templates netascode modules use.
//
//   - CLITemplate ("cli"): the template body is an IOS-XE CLI snippet
//     (typically Jinja/HCL-shaped) that is rendered to device-native
//     CLI text and pushed through a CLI-capable transport. The CRD
//     surface for this value is present now so operators can author
//     CLI templates without a later schema migration; the render and
//     transport path lands in a subsequent phase alongside NETCONF
//     (see docs/rfcs/config-driver-review-feedback.md, feedback 3b).
//
// +kubebuilder:validation:Enum=data-model;cli
type TemplateKind string

// Supported template kinds.
const (
	DataModelTemplate TemplateKind = "data-model"
	CLITemplate       TemplateKind = "cli"
)

// IOSXETemplateSpec declares a parameterised configuration fragment.
type IOSXETemplateSpec struct {
	// Type selects between the two netascode template styles. Default
	// is "data-model" so existing templates keep working without any
	// CR change.
	// +kubebuilder:default=data-model
	// +optional
	Type TemplateKind `json:"type,omitempty"`

	// Parameters is the contract the referring CR supplies values for.
	// +optional
	Parameters []TemplateParameter `json:"parameters,omitempty"`

	// Configuration is the template body. Shape depends on Type:
	//
	//   - For Type=data-model: a parameterised netascode YAML fragment.
	//     The expander substitutes {{ .Name }} references using the Go
	//     text/template engine restricted to parameter lookups (no
	//     function calls, no file access), and the result merges into
	//     the resolved intent tree.
	//
	//   - For Type=cli: an IOS-XE CLI snippet. The CRD admits the body
	//     today; render and transport are deferred — a Type=cli
	//     template is rejected at resolve time with a clear error so
	//     operators see the gap at admission, not at silent skip.
	//
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Configuration runtime.RawExtension `json:"configuration"`
}

// IOSXETemplateStatus tracks how many IOSXEConfig CRs reference this template.
type IOSXETemplateStatus struct {
	// ObservedGeneration is the .metadata.generation the aggregator last read.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Referencers counts IOSXEConfig CRs whose spec.templateRefs include
	// this template by name.
	// +optional
	Referencers int32 `json:"referencers,omitempty"`

	// Conditions follows the standard Kubernetes conditions shape.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// IOSXETemplate is a reusable, parameterised configuration fragment.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=iosxetpl
// +kubebuilder:printcolumn:name="Refs",type=integer,JSONPath=`.status.referencers`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXETemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXETemplateSpec   `json:"spec"`
	Status IOSXETemplateStatus `json:"status,omitempty"`
}

// IOSXETemplateList is the list type for IOSXETemplate.
//
// +kubebuilder:object:root=true
type IOSXETemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXETemplate `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXETemplate{}, &IOSXETemplateList{})
}
