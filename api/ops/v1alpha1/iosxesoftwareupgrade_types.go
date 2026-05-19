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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// UpgradePhase enumerates the IOSXESoftwareUpgrade state machine.
//
// Pending → preflight checks
// Resolving → image fetch + sha256 verify
// Transferring → gNOI OS.Install bidi stream upload
// Validating → wait for device-side Validated message
// Activating → gNOI OS.Activate; per spec this reboots the device unless NoReboot
// AwaitingReachability → device unreachable while it boots the new image
// Verifying → gNOI OS.Verify + gNMI cross-check
// RollingBack → activating the previously observed version after verify failure
// Terminal phases: Succeeded, Failed, PreflightFailed, ValidationFailed,
// RolledBack, RebootTimeout, Cancelled.
//
// +kubebuilder:validation:Enum=Pending;Resolving;Transferring;TransferInterrupted;Validating;Activating;AwaitingReachability;Verifying;RollingBack;Succeeded;Failed;PreflightFailed;ValidationFailed;RolledBack;RebootTimeout;Cancelled
type UpgradePhase string

const (
	UpgradePhasePending              UpgradePhase = "Pending"
	UpgradePhaseResolving            UpgradePhase = "Resolving"
	UpgradePhaseTransferring         UpgradePhase = "Transferring"
	UpgradePhaseTransferInterrupted  UpgradePhase = "TransferInterrupted"
	UpgradePhaseValidating           UpgradePhase = "Validating"
	UpgradePhaseActivating           UpgradePhase = "Activating"
	UpgradePhaseAwaitingReachability UpgradePhase = "AwaitingReachability"
	UpgradePhaseVerifying            UpgradePhase = "Verifying"
	UpgradePhaseRollingBack          UpgradePhase = "RollingBack"
	UpgradePhaseSucceeded            UpgradePhase = "Succeeded"
	UpgradePhaseFailed               UpgradePhase = "Failed"
	UpgradePhasePreflightFailed      UpgradePhase = "PreflightFailed"
	UpgradePhaseValidationFailed     UpgradePhase = "ValidationFailed"
	UpgradePhaseRolledBack           UpgradePhase = "RolledBack"
	UpgradePhaseRebootTimeout        UpgradePhase = "RebootTimeout"
	UpgradePhaseCancelled            UpgradePhase = "Cancelled"
)

// UpgradeStrategy controls how the activate step is sequenced.
//
// +kubebuilder:validation:Enum=Reload;ISSU;NoReboot
type UpgradeStrategy string

const (
	// UpgradeStrategyReload — gNOI OS.Activate with NoReboot=false.
	// The device performs install activate + commit and reloads.
	UpgradeStrategyReload UpgradeStrategy = "Reload"

	// UpgradeStrategyISSU — same Activate call as Reload, but the
	// reconciler asserts post-Verify that the device chose the ISSU
	// upgrade path (cross-checked via gNMI Get on install-oper).
	// Strategy is a *preference* — the device makes the final call
	// based on dual-RP and version compatibility.
	UpgradeStrategyISSU UpgradeStrategy = "ISSU"

	// UpgradeStrategyNoReboot — gNOI OS.Activate with NoReboot=true.
	// Device stages the new image for next boot but does NOT reboot
	// itself. Reconciler terminates as Succeeded; operator triggers
	// the reload via a separate IOSXEOperationalAction (Phase D).
	UpgradeStrategyNoReboot UpgradeStrategy = "NoReboot"
)

// IOSXESoftwareUpgrade drives a multi-phase IOS-XE image upgrade via
// gNOI OS.Install / Activate / Verify. One CR per upgrade operation;
// owner-referenced to the target CiscoDevice so a device delete
// cascades.
//
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=xeupgrade
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Device",type=string,JSONPath=`.spec.deviceRef.name`
// +kubebuilder:printcolumn:name="Target",type=string,JSONPath=`.spec.targetVersion`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Running",type=string,JSONPath=`.status.runningVersion`
// +kubebuilder:printcolumn:name="Progress",type=integer,JSONPath=`.status.transferProgress.percent`,priority=1
// +kubebuilder:printcolumn:name="Message",type=string,JSONPath=`.status.message`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
type IOSXESoftwareUpgrade struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   IOSXESoftwareUpgradeSpec   `json:"spec,omitempty"`
	Status IOSXESoftwareUpgradeStatus `json:"status,omitempty"`
}

// IOSXESoftwareUpgradeSpec declares an upgrade request.
type IOSXESoftwareUpgradeSpec struct {
	// DeviceRef targets the CiscoDevice the upgrade runs against.
	// +kubebuilder:validation:Required
	DeviceRef configv1alpha1.DeviceRef `json:"deviceRef"`

	// ImageSource is where the reconciler obtains the SPA bundle bytes.
	// +kubebuilder:validation:Required
	ImageSource UpgradeImageSource `json:"imageSource"`

	// TargetVersion is the version string the device's gNOI OS server
	// uses to identify the staged image. The reconciler:
	//   - asserts the Validated message names this version
	//   - passes it to gNOI OS.Activate as the version parameter
	//   - cross-checks via OS.Verify in the Verifying phase
	//
	// IOS-XE reports image versions in several shapes depending on the
	// release and CLI surface. Examples that this field accepts:
	//   - "17.15.01a" (release-format)
	//   - "26.01.01"  (build-number trimmed)
	//   - "26.01.01.0.340" (full "show install summary" form)
	//   - "17.18.02.0.4112.1766116039" (oper-data form)
	//
	// The reconciler matches the device-reported version with a prefix-
	// aware comparison (Verify result == TargetVersion, or starts with
	// TargetVersion + "."), so operators may specify the shortest form
	// that's still unambiguous within the device's installed images.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)+([a-z])?$`
	TargetVersion string `json:"targetVersion"`

	// Strategy controls whether Activate performs the reload itself
	// (default Reload), asserts an ISSU path, or stages without
	// rebooting (NoReboot).
	// +optional
	// +kubebuilder:default=Reload
	Strategy UpgradeStrategy `json:"strategy,omitempty"`

	// RollbackOnFailure, when true, attempts to reactivate the version
	// that was running before this upgrade began when post-Verify reports
	// a different version than TargetVersion. Default true.
	// +optional
	// +kubebuilder:default=true
	RollbackOnFailure *bool `json:"rollbackOnFailure,omitempty"`

	// MaintenanceWindow bounds when the reconciler will begin the
	// Transferring phase. Outside the window, the CR sits in Pending
	// without consuming device cycles.
	// +optional
	MaintenanceWindow *UpgradeWindow `json:"maintenanceWindow,omitempty"`

	// ResumePolicy controls how TransferInterrupted is handled.
	// +optional
	// +kubebuilder:default=Retry
	// +kubebuilder:validation:Enum=Retry;Abort
	ResumePolicy string `json:"resumePolicy,omitempty"`

	// MaxRetries caps the number of TransferInterrupted → Transferring
	// retries before terminal Failed. Default 3.
	// +optional
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	MaxRetries int32 `json:"maxRetries,omitempty"`

	// RebootTimeoutSeconds caps how long the reconciler will wait in
	// AwaitingReachability before declaring RebootTimeout. Default 1800.
	// +optional
	// +kubebuilder:default=1800
	// +kubebuilder:validation:Minimum=60
	RebootTimeoutSeconds int32 `json:"rebootTimeoutSeconds,omitempty"`
}

// UpgradeImageSource is a discriminated union of supported image
// sources. Exactly one of URL, ConfigMapRef, or LocalPath should be
// set. The reconciler rejects spec violations in Pending preflight.
type UpgradeImageSource struct {
	// URL is a remote image URI the reconciler fetches before streaming
	// the bytes to the device with gNOI OS.Install. Supported schemes are
	// http, https, tftp, ftp, scp, and sftp. Required SHA256 verification
	// — declared via SHA256 below.
	// +optional
	// +kubebuilder:validation:Pattern=`^(https?|tftp|ftp|scp|sftp)://`
	URL string `json:"url,omitempty"`

	// SHA256 is the lowercase hex SHA-256 digest the reconciler asserts
	// after fetching the URL. Required when URL is set.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	SHA256 string `json:"sha256,omitempty"`

	// URLSecretRef optionally names a Secret in the same namespace with
	// transfer credentials. FTP may use username/password; SCP and SFTP
	// may use username/password or username/privateKey/passphrase. SCP
	// and SFTP host-key verification uses knownHosts or known_hosts from
	// this Secret unless the URL query includes insecureSkipHostKey=true.
	// URL-embedded userinfo takes precedence over Secret credentials.
	// +optional
	URLSecretRef *corev1.LocalObjectReference `json:"urlSecretRef,omitempty"`

	// ConfigMapRef names a ConfigMap whose binaryData["image"] holds
	// the bytes. Capped at ~900 KiB by Kubernetes — testing only.
	// +optional
	ConfigMapRef *corev1.LocalObjectReference `json:"configMapRef,omitempty"`

	// LocalPath assumes the image is already on the device flash. The
	// reconciler skips Resolving + Transferring and jumps to Activating.
	// Path must be an absolute IOS-XE filesystem path.
	// +optional
	// +kubebuilder:validation:Pattern=`^(flash|bootflash|harddisk):`
	LocalPath string `json:"localPath,omitempty"`

	// LocalPathSHA256 optionally verifies the staged flash image before
	// activation by reading it through gNOI File.Get and comparing the
	// device-supplied SHA256. Strongly recommended for production LocalPath
	// upgrades.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	LocalPathSHA256 string `json:"localPathSHA256,omitempty"`
}

// UpgradeWindow bounds when the upgrade may proceed past Pending.
type UpgradeWindow struct {
	// NotBefore is the earliest time the reconciler may enter Resolving.
	// +optional
	NotBefore *metav1.Time `json:"notBefore,omitempty"`
	// NotAfter is the latest time the reconciler may enter Resolving.
	// Past this, the CR transitions to terminal Failed with reason
	// MaintenanceWindowExpired.
	// +optional
	NotAfter *metav1.Time `json:"notAfter,omitempty"`
}

// IOSXESoftwareUpgradeStatus carries observed state.
type IOSXESoftwareUpgradeStatus struct {
	// Phase is the current state-machine position.
	// +optional
	Phase UpgradePhase `json:"phase,omitempty"`

	// ObservedGeneration mirrors spec.generation that produced this status.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions surface granular state. Type "Ready" follows the
	// standard "True/False/Unknown" convention; intermediate
	// conditions (e.g. "ImageResolved", "Transferred", "Activated",
	// "DeviceReachable", "Verified") are set as the state machine
	// advances.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TransferProgress reports cumulative bytes uploaded during the
	// Transferring phase. Cleared on Validated.
	// +optional
	TransferProgress *UpgradeTransferProgress `json:"transferProgress,omitempty"`

	// RunningVersion is the device-reported running OS version after
	// Verifying.
	// +optional
	RunningVersion string `json:"runningVersion,omitempty"`

	// PreviousVersion is the device-reported OS version captured before
	// image activation. It is used as the rollback target when
	// RollbackOnFailure is enabled.
	// +optional
	PreviousVersion string `json:"previousVersion,omitempty"`

	// ValidatedVersion is the exact device-reported OS version returned
	// by gNOI OS.Install Validated. IOS XE may require this exact value
	// on OS.Activate even when the requested TargetVersion is a shorter
	// release prefix.
	// +optional
	ValidatedVersion string `json:"validatedVersion,omitempty"`

	// StartTime is when the reconciler first transitioned out of Pending.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// CompletionTime is when the reconciler entered a terminal phase.
	// +optional
	CompletionTime *metav1.Time `json:"completionTime,omitempty"`

	// FailureReason holds a short machine-readable reason on Failed /
	// PreflightFailed / ValidationFailed / RolledBack / RebootTimeout.
	// +optional
	FailureReason string `json:"failureReason,omitempty"`

	// Message is the human-readable status message. Mirrored on the
	// Ready condition.
	// +optional
	Message string `json:"message,omitempty"`

	// RetryCount tracks TransferInterrupted retries.
	// +optional
	RetryCount int32 `json:"retryCount,omitempty"`
}

// UpgradeTransferProgress reports per-phase byte counts.
type UpgradeTransferProgress struct {
	BytesTransferred int64 `json:"bytesTransferred"`
	TotalBytes       int64 `json:"totalBytes,omitempty"`
	// Percent is BytesTransferred/TotalBytes×100, rounded down. Zero
	// when TotalBytes is unknown.
	Percent int32 `json:"percent,omitempty"`
}

// IOSXESoftwareUpgradeList is the list type.
//
// +kubebuilder:object:root=true
type IOSXESoftwareUpgradeList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []IOSXESoftwareUpgrade `json:"items"`
}

func init() {
	SchemeBuilder.Register(&IOSXESoftwareUpgrade{}, &IOSXESoftwareUpgradeList{})
}
