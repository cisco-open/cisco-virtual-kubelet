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
// Every kind on this enum is read-only by API contract. Destructive
// gNOI operations (Reboot, FactoryReset, FileRemove, CertRotate, etc.)
// live on the separate IOSXEOperationalAction CRD with distinct RBAC,
// so this enum continues to describe a safe, query-shaped surface
// that operators can grant to dashboards or low-trust automation
// without giving them mutation power on the device.
//
// +kubebuilder:validation:Enum=ShowCommand;ConfigDiff;PacketCapture;GNOIPing;GNOITraceroute;GNOITime;GNOIFileGet;GNOIFileStat;GNOICertGet;GNOICanGenerateCSR;GNOIRebootStatus;GNOIOSVerify
type OperationKind string

const (
	// OperationKindShowCommand runs read-only IOS-XE operational commands.
	OperationKindShowCommand OperationKind = "ShowCommand"
	// OperationKindConfigDiff is reserved for a read-only diff operation.
	OperationKindConfigDiff OperationKind = "ConfigDiff"
	// OperationKindPacketCapture is reserved for a read-only capture operation.
	OperationKindPacketCapture OperationKind = "PacketCapture"

	// gNOI read-only operations. All produce structured output.

	// OperationKindGNOIPing runs gNOI System.Ping and captures the
	// streamed responses (per-probe RTT plus summary).
	OperationKindGNOIPing OperationKind = "GNOIPing"
	// OperationKindGNOITraceroute runs gNOI System.Traceroute.
	OperationKindGNOITraceroute OperationKind = "GNOITraceroute"
	// OperationKindGNOITime returns the device clock from gNOI System.Time.
	OperationKindGNOITime OperationKind = "GNOITime"
	// OperationKindGNOIFileGet streams a file off the device via gNOI File.Get.
	OperationKindGNOIFileGet OperationKind = "GNOIFileGet"
	// OperationKindGNOIFileStat returns gNOI File.Stat metadata.
	OperationKindGNOIFileStat OperationKind = "GNOIFileStat"
	// OperationKindGNOICertGet returns gNOI Cert.GetCertificates output.
	OperationKindGNOICertGet OperationKind = "GNOICertGet"
	// OperationKindGNOICanGenerateCSR asks the device whether it can produce a CSR.
	OperationKindGNOICanGenerateCSR OperationKind = "GNOICanGenerateCSR"
	// OperationKindGNOIRebootStatus returns gNOI System.RebootStatus.
	OperationKindGNOIRebootStatus OperationKind = "GNOIRebootStatus"
	// OperationKindGNOIOSVerify returns gNOI OS.Verify (running version + activation message).
	OperationKindGNOIOSVerify OperationKind = "GNOIOSVerify"
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

	// Args is a string bag for operation-specific parameters.
	//
	// For ShowCommand, args.command or args.commands may be used when
	// Commands is not set.
	//
	// For PacketCapture, args.name (or args.capture) names an existing
	// monitor-capture session and the reconciler synthesises only the
	// exact `show monitor capture <name> buffer dump` command. The
	// historical args.command escape hatch was removed because it
	// flowed through the diagnostic allowlist which permits broad
	// monitor/terminal/test head-words; PacketCapture is documented as
	// read-only and the allowlist was wider than that contract.
	// Callers that need other monitor invocations must use ShowCommand
	// with explicit Commands.
	// +optional
	Args map[string]string `json:"args,omitempty"`

	// Commands is the ordered ShowCommand command list. Every command is
	// checked by the server-side read-only allowlist before any device RPC.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:items:Pattern=`^(show|more|dir|ping|ping6|traceroute|traceroute6|monitor|test|verify|calendar|terminal|namespace)( |$)`
	Commands []string `json:"commands,omitempty"`

	// GNOI carries typed parameters for gNOI-backed operation kinds.
	// Ignored by ShowCommand/ConfigDiff/PacketCapture.
	// +optional
	GNOI *GNOIArgs `json:"gnoi,omitempty"`
}

// GNOIArgs holds typed inputs for the gNOI-backed read-only operation
// kinds. Each kind reads at most one of the inner fields; unused ones
// are ignored. Destructive gNOI inputs live on IOSXEOperationalAction
// instead.
type GNOIArgs struct {
	// Ping carries parameters for OperationKindGNOIPing.
	// +optional
	Ping *GNOIPingArgs `json:"ping,omitempty"`

	// Traceroute carries parameters for OperationKindGNOITraceroute.
	// +optional
	Traceroute *GNOITracerouteArgs `json:"traceroute,omitempty"`

	// File carries parameters for OperationKindGNOIFileGet and
	// OperationKindGNOIFileStat.
	// +optional
	File *GNOIFileArgs `json:"file,omitempty"`

	// Cert carries parameters for OperationKindGNOICanGenerateCSR.
	// (OperationKindGNOICertGet takes no inputs.)
	// +optional
	Cert *GNOICertArgs `json:"cert,omitempty"`
}

// GNOIPingArgs mirrors PingOpts on the wire.
type GNOIPingArgs struct {
	// +kubebuilder:validation:Required
	Destination string `json:"destination"`
	// +optional
	Source string `json:"source,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000
	Count int32 `json:"count,omitempty"`
	// IntervalMillis between probes; 0 = device default.
	// +optional
	// +kubebuilder:validation:Minimum=0
	IntervalMillis int32 `json:"intervalMillis,omitempty"`
	// WaitMillis per probe; 0 = device default.
	// +optional
	// +kubebuilder:validation:Minimum=0
	WaitMillis int32 `json:"waitMillis,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=0
	Size int32 `json:"size,omitempty"`
}

// GNOITracerouteArgs mirrors TracerouteOpts on the wire.
type GNOITracerouteArgs struct {
	// +kubebuilder:validation:Required
	Destination string `json:"destination"`
	// +optional
	Source string `json:"source,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=255
	MaxHops int32 `json:"maxHops,omitempty"`
	// +optional
	// +kubebuilder:validation:Enum=ICMP;UDP;TCP
	Protocol string `json:"protocol,omitempty"`
	// +optional
	WaitMillis int32 `json:"waitMillis,omitempty"`
}

// GNOIFileArgs mirrors the File.Get / File.Stat inputs.
type GNOIFileArgs struct {
	// Path is the absolute IOS-XE filesystem path (e.g.
	// "flash:cat9k.bin"). Required; rejected when missing a recognised
	// filesystem prefix.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^(flash|bootflash|harddisk|usbflash0|usbflash1|crashinfo|nvram|webui):`
	Path string `json:"path"`

	// MaxBytes caps the inlined output of FileGet. Files larger than
	// this are spilled to a ConfigMap (reusing the existing artefact-
	// spill path); 0 means use the reconciler default (256 KiB).
	// +optional
	// +kubebuilder:validation:Minimum=0
	MaxBytes int64 `json:"maxBytes,omitempty"`
}

// GNOICertArgs mirrors CanGenerateCSROpts on the wire.
type GNOICertArgs struct {
	// +optional
	// +kubebuilder:validation:Enum=RT_RSA
	KeyType string `json:"keyType,omitempty"`
	// +optional
	// +kubebuilder:validation:Enum=CT_X509
	CertificateType string `json:"certificateType,omitempty"`
	// +optional
	// +kubebuilder:validation:Minimum=1024
	// +kubebuilder:validation:Maximum=8192
	KeySize uint32 `json:"keySize,omitempty"`
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
