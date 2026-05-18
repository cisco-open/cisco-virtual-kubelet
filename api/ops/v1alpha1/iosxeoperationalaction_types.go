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

// ActionKind enumerates the write-class gNOI operations exposed via
// IOSXEOperationalAction. These are all device-mutating RPCs; the CRD
// lives in a separate group from the read-only DeviceOperation so
// operators can grant low-trust automation read access without
// implicitly granting reboot / factory-reset / file-write.
//
// +kubebuilder:validation:Enum=Reboot;CancelReboot;KillProcess;FilePut;FileRemove;FactoryReset
type ActionKind string

const (
	ActionKindReboot       ActionKind = "Reboot"
	ActionKindCancelReboot ActionKind = "CancelReboot"
	ActionKindKillProcess  ActionKind = "KillProcess"
	ActionKindFilePut      ActionKind = "FilePut"
	ActionKindFileRemove   ActionKind = "FileRemove"
	ActionKindFactoryReset ActionKind = "FactoryReset"
)

// ActionPhase reports the lifecycle state of an action.
//
// +kubebuilder:validation:Enum=Pending;Running;Succeeded;Failed;Rejected
type ActionPhase string

const (
	ActionPhasePending   ActionPhase = "Pending"
	ActionPhaseRunning   ActionPhase = "Running"
	ActionPhaseSucceeded ActionPhase = "Succeeded"
	ActionPhaseFailed    ActionPhase = "Failed"
	ActionPhaseRejected  ActionPhase = "Rejected" // failed pre-flight (Confirm mismatch, etc.)
)

// IOSXEOperationalAction is a one-shot write-class operation against
// one IOS-XE device. The reconciler executes exactly once: it does
// not retry on transient failure, because every kind is destructive
// or near-destructive (Reboot / FactoryReset / FileRemove / etc.).
// Operators submit a new CR to re-attempt.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=xeop
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Kind",type=string,JSONPath=`.spec.action.kind`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXEOperationalAction struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXEOperationalActionSpec   `json:"spec,omitempty"`
	Status IOSXEOperationalActionStatus `json:"status,omitempty"`
}

// IOSXEOperationalActionSpec declares a write-class operation.
type IOSXEOperationalActionSpec struct {
	// DeviceRef targets the CiscoDevice the action runs against.
	// +kubebuilder:validation:Required
	DeviceRef configv1alpha1.DeviceRef `json:"deviceRef"`

	// Action is the requested action plus typed inputs.
	// +kubebuilder:validation:Required
	Action ActionRequest `json:"action"`

	// Confirm must equal the target device's metadata.name. This is a
	// safety guard against operators applying an action against the
	// wrong device by typo (mirrors `kubectl drain --force` style).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Confirm string `json:"confirm"`
}

// ActionRequest carries the kind + typed inputs.
type ActionRequest struct {
	// Kind names the action variant.
	// +kubebuilder:validation:Required
	Kind ActionKind `json:"kind"`

	// Reboot is the input for ActionKindReboot.
	// +optional
	Reboot *RebootActionArgs `json:"reboot,omitempty"`

	// CancelReboot is the input for ActionKindCancelReboot.
	// +optional
	CancelReboot *CancelRebootArgs `json:"cancelReboot,omitempty"`

	// KillProcess is the input for ActionKindKillProcess.
	// +optional
	KillProcess *KillProcessArgs `json:"killProcess,omitempty"`

	// FilePut is the input for ActionKindFilePut.
	// +optional
	FilePut *FilePutArgs `json:"filePut,omitempty"`

	// FileRemove is the input for ActionKindFileRemove.
	// +optional
	FileRemove *FileRemoveArgs `json:"fileRemove,omitempty"`

	// FactoryReset is the input for ActionKindFactoryReset.
	// +optional
	FactoryReset *FactoryResetArgs `json:"factoryReset,omitempty"`
}

// RebootActionArgs mirrors gnoi.RebootOpts.
type RebootActionArgs struct {
	// +optional
	// +kubebuilder:validation:Enum=COLD;NSF;POWERDOWN;HALT;WARM
	// +kubebuilder:default=COLD
	Method string `json:"method,omitempty"`
	// DelaySeconds before the reboot fires. Zero means immediate.
	// +optional
	// +kubebuilder:validation:Minimum=0
	DelaySeconds int64 `json:"delaySeconds,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	Force bool `json:"force,omitempty"`
}

// CancelRebootArgs mirrors the CancelReboot inputs.
type CancelRebootArgs struct {
	// +optional
	Message string `json:"message,omitempty"`
}

// KillProcessArgs mirrors gnoi.KillProcessOpts.
type KillProcessArgs struct {
	// +optional
	PID uint32 `json:"pid,omitempty"`
	// +optional
	Name string `json:"name,omitempty"`
	// +optional
	// +kubebuilder:validation:Enum=TERM;KILL;HUP;ABRT
	// +kubebuilder:default=TERM
	Signal string `json:"signal,omitempty"`
	// +optional
	Restart bool `json:"restart,omitempty"`
}

// FilePutArgs carries the path + ConfigMap-source bytes for FilePut.
// Phase D ships ConfigMap-only sources; URL-backed file pushes follow
// the same SHA256 pattern as IOSXESoftwareUpgrade.imageSource and can
// be added later.
type FilePutArgs struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^(flash|bootflash|harddisk|usbflash0|usbflash1):`
	Path string `json:"path"`

	// ConfigMapName names a ConfigMap in the same namespace whose
	// binaryData["content"] holds the bytes.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ConfigMapName string `json:"configMapName"`

	// Permissions is the UNIX octal mode (default 0o644).
	// +optional
	Permissions uint32 `json:"permissions,omitempty"`
}

// FileRemoveArgs targets a single file on the device.
type FileRemoveArgs struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^(flash|bootflash|harddisk|usbflash0|usbflash1):`
	Path string `json:"path"`
}

// FactoryResetArgs mirrors the Start RPC flags. RetainCerts defaults
// to true so the operator's onboarding trustpoint survives.
type FactoryResetArgs struct {
	// +optional
	FactoryOS bool `json:"factoryOS,omitempty"`
	// +optional
	ZeroFill bool `json:"zeroFill,omitempty"`
	// +optional
	// +kubebuilder:default=true
	RetainCerts *bool `json:"retainCerts,omitempty"`
}

// IOSXEOperationalActionStatus carries observed state.
type IOSXEOperationalActionStatus struct {
	// +optional
	Phase ActionPhase `json:"phase,omitempty"`
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`
	// +optional
	Message string `json:"message,omitempty"`
	// +optional
	FailureReason string `json:"failureReason,omitempty"`
	// Result holds structured device-side output where applicable
	// (e.g. RebootStatus snapshot post-CancelReboot). JSON-encoded.
	// +optional
	Result string `json:"result,omitempty"`
}

// IOSXEOperationalActionList is the list type.
//
// +kubebuilder:object:root=true
type IOSXEOperationalActionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXEOperationalAction `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXEOperationalAction{}, &IOSXEOperationalActionList{})
}
