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

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// OperationKind identifies the requested device operation.
//
// +kubebuilder:validation:Enum=ShowCommand;ConfigDiff;PacketCapture
type OperationKind string

const (
	// OperationKindShowCommand runs read-only IOS-XE operational commands.
	OperationKindShowCommand OperationKind = "ShowCommand"
	// OperationKindConfigDiff is reserved for a read-only diff operation.
	OperationKindConfigDiff OperationKind = "ConfigDiff"
	// OperationKindPacketCapture is reserved for a read-only capture operation.
	OperationKindPacketCapture OperationKind = "PacketCapture"
)

// OperationPhase reports the lifecycle state of a DeviceOperation.
//
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Cancelled
type OperationPhase string

const (
	OperationPhasePending   OperationPhase = "Pending"
	OperationPhaseRunning   OperationPhase = "Running"
	OperationPhaseSucceeded OperationPhase = "Succeeded"
	OperationPhaseFailed    OperationPhase = "Failed"
	OperationPhaseCancelled OperationPhase = "Cancelled"
)

// DeviceOperationSpec declares a bounded asynchronous operation against one
// CiscoDevice. v1alpha1 is intentionally read-only: ShowCommand, ConfigDiff,
// and existing PacketCapture buffer reads. Write-class operations are a later
// RBAC/admission phase.
type DeviceOperationSpec struct {
	// DeviceRef targets the CiscoDevice the operation runs against.
	// +kubebuilder:validation:Required
	DeviceRef configv1alpha1.DeviceRef `json:"deviceRef"`

	// Operation is the requested operation kind plus operation-specific inputs.
	// +kubebuilder:validation:Required
	Operation DeviceOperationRequest `json:"operation"`

	// TTLSecondsAfterFinished requests best-effort cleanup after the operation
	// reaches a terminal phase. Zero or nil disables automatic cleanup.
	// +optional
	// +kubebuilder:validation:Minimum=0
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`
}

// DeviceOperationRequest carries operation-specific parameters.
type DeviceOperationRequest struct {
	// Kind names the read-only operation.
	// +kubebuilder:validation:Required
	Kind OperationKind `json:"kind"`

	// Args is a string bag for operation-specific parameters. For ShowCommand,
	// args.command or args.commands may be used when Commands is not set.
	// +optional
	Args map[string]string `json:"args,omitempty"`

	// Commands is the ordered ShowCommand command list. Every command is
	// checked by the server-side read-only allowlist before any device RPC.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:Pattern=`^(show|more|dir|ping|ping6|traceroute|traceroute6|monitor|test|verify|calendar|terminal|namespace)( |$)`
	Commands []string `json:"commands,omitempty"`
}

// DeviceOperationStatus reports operation progress and any small inline
// results. Large artifact sinks are reserved for later operation kinds.
type DeviceOperationStatus struct {
	// Phase summarises the operation lifecycle.
	// +optional
	Phase OperationPhase `json:"phase,omitempty"`

	// ObservedGeneration is the .metadata.generation the reconciler last acted
	// on.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// StartTime records when the current generation began executing.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime records when the current generation reached a terminal
	// phase.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// Message is a short human-readable terminal or waiting reason.
	// +optional
	Message string `json:"message,omitempty"`

	// ArtifactURIs references out-of-band operation artifacts. ShowCommand keeps
	// small output inline; future capture/diff reconcilers can populate this.
	// +optional
	ArtifactURIs []string `json:"artifactURIs,omitempty"`

	// Outputs holds inline ShowCommand results.
	// +optional
	Outputs []DeviceOperationOutput `json:"outputs,omitempty"`

	// Conditions reports Ready for consumers that prefer condition-based reads.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
}

// DeviceOperationOutput records one command's output and post-processing flags.
type DeviceOperationOutput struct {
	// Command is the input command.
	Command string `json:"command"`

	// Output is the redacted and possibly truncated output.
	// +optional
	Output string `json:"output,omitempty"`

	// Err carries a per-command failure from the device.
	// +optional
	Err string `json:"err,omitempty"`

	// Truncated is true when Output was clipped to the inline size limit.
	// +optional
	Truncated bool `json:"truncated,omitempty"`

	// Redacted is true when the secret-redaction filter removed content.
	// +optional
	Redacted bool `json:"redacted,omitempty"`
}

// DeviceOperation is the auditable async operation CRD. It is intentionally
// independent of Virtual Kubelet's Pod lifecycle APIs.
//
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=devop
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.spec.operation.kind`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type DeviceOperation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DeviceOperationSpec   `json:"spec"`
	Status DeviceOperationStatus `json:"status,omitempty"`
}

// DeviceOperationList is the list type for DeviceOperation.
//
// +kubebuilder:object:root=true
type DeviceOperationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []DeviceOperation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&DeviceOperation{}, &DeviceOperationList{})
}
