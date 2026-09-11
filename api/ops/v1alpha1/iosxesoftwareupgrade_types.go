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
// Resolving → gNOI preflight and source/inventory resolution
// Staging → platform-native device-file registration claim and submission
// Transferring → gNOI OS.Install bidi stream upload
// Validating → wait for gNOI or native install-inventory convergence
// Activating → gNOI OS.Activate; per spec this reboots the device unless NoReboot
// AwaitingReachability → device unreachable while it boots the new image
// Verifying → gNOI OS.Verify running-version check
// RollingBack → activating the previously observed version after verify failure
// Terminal phases: Succeeded, StagedForNextBoot, Failed, PreflightFailed,
// ValidationFailed, RolledBack, RebootTimeout, Cancelled.
type UpgradePhase string

const (
	UpgradePhasePending              UpgradePhase = "Pending"
	UpgradePhaseResolving            UpgradePhase = "Resolving"
	UpgradePhaseStaging              UpgradePhase = "Staging"
	UpgradePhaseTransferring         UpgradePhase = "Transferring"
	UpgradePhaseTransferInterrupted  UpgradePhase = "TransferInterrupted"
	UpgradePhaseValidating           UpgradePhase = "Validating"
	UpgradePhaseActivating           UpgradePhase = "Activating"
	UpgradePhaseAwaitingReachability UpgradePhase = "AwaitingReachability"
	UpgradePhaseVerifying            UpgradePhase = "Verifying"
	UpgradePhaseRollingBack          UpgradePhase = "RollingBack"
	UpgradePhaseSucceeded            UpgradePhase = "Succeeded"
	UpgradePhaseStagedForNextBoot    UpgradePhase = "StagedForNextBoot"
	UpgradePhaseFailed               UpgradePhase = "Failed"
	UpgradePhasePreflightFailed      UpgradePhase = "PreflightFailed"
	UpgradePhaseValidationFailed     UpgradePhase = "ValidationFailed"
	UpgradePhaseRolledBack           UpgradePhase = "RolledBack"
	UpgradePhaseRebootTimeout        UpgradePhase = "RebootTimeout"
	// UpgradePhaseCancelled is retained for compatibility with upgrade objects
	// completed by earlier controllers. The current reconciler does not create
	// this phase because IOS XE exposes no reliable cancellation operation.
	UpgradePhaseCancelled UpgradePhase = "Cancelled"
)

// UpgradeStrategy controls how the activate step is sequenced.
//
// +kubebuilder:validation:Enum=Reload;ISSU;NoReboot
type UpgradeStrategy string

const (
	// UpgradeStrategyReload — gNOI OS.Activate with NoReboot=false.
	// The device performs install activate + commit and reloads.
	UpgradeStrategyReload UpgradeStrategy = "Reload"

	// UpgradeStrategyISSU is reserved for a future platform lifecycle
	// capability that can request and verify IOS XE's ISSU path. The
	// reconciler currently rejects this strategy during preflight instead
	// of silently performing a reload activation.
	UpgradeStrategyISSU UpgradeStrategy = "ISSU"

	// UpgradeStrategyNoReboot — gNOI OS.Activate with NoReboot=true.
	// If the target is already running, the reconciler succeeds without
	// activation. Otherwise the device stages the new image for next boot but
	// does NOT reboot itself, and the reconciler terminates as
	// StagedForNextBoot. The operator triggers the reload via a separate
	// IOSXEOperationalAction (Phase D).
	UpgradeStrategyNoReboot UpgradeStrategy = "NoReboot"
)

// IOSXESoftwareUpgrade drives a multi-phase IOS-XE image upgrade via
// gNOI OS.Install / Activate / Verify. One CR represents one explicitly
// authorized upgrade operation against the referenced CiscoDevice.
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
//
// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="IOSXESoftwareUpgrade spec is immutable after creation"
type IOSXESoftwareUpgradeSpec struct {
	// DeviceRef targets the CiscoDevice the upgrade runs against.
	// +kubebuilder:validation:Required
	DeviceRef configv1alpha1.DeviceRef `json:"deviceRef"`

	// ImageSource selects how the target image enters the device install
	// lifecycle: transfer external bytes, use a preinstalled version, or
	// register a device-resident file.
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
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^[0-9]+(\.[0-9]+)+([a-z])?$`
	TargetVersion string `json:"targetVersion"`

	// Strategy controls whether Activate performs the reload itself
	// (default Reload), requests the currently unsupported ISSU strategy, or stages without
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

	// MaintenanceWindow gates the start of lifecycle work and every device
	// mutation that has not yet been claimed. It cannot stop a mutation already
	// submitted or in flight.
	// +optional
	MaintenanceWindow *UpgradeWindow `json:"maintenanceWindow,omitempty"`

	// ResumePolicy is retained for wire compatibility with earlier manifests.
	// The current at-most-once reconciler never retries a device mutation based
	// on this value.
	// Deprecated: retained for storage compatibility; ignored by the reconciler.
	// +optional
	// +kubebuilder:default=Retry
	// +kubebuilder:validation:Enum=Retry;Abort
	ResumePolicy string `json:"resumePolicy,omitempty"`

	// MaxRetries is retained for wire compatibility with earlier manifests.
	// The current at-most-once reconciler never retries a device mutation based
	// on this value.
	// Deprecated: retained for storage compatibility; ignored by the reconciler.
	// +optional
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=10
	MaxRetries int32 `json:"maxRetries,omitempty"`

	// InstallTimeoutSeconds bounds two consecutive stages: first pre-install
	// gNOI readiness, source resolution, and device-file hash verification;
	// then, from the first OS.Install or native-registration claim, one shared
	// deadline for every per-supervisor install or registration and inventory
	// convergence. Default 3600.
	// +optional
	// +kubebuilder:default=3600
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=86400
	InstallTimeoutSeconds int32 `json:"installTimeoutSeconds,omitempty"`

	// RebootTimeoutSeconds starts at the first activation claim and bounds the
	// complete activation, reachability, and final-verification sequence. A
	// rollback uses the same duration as an independent convergence deadline.
	// Default 1800.
	// +optional
	// +kubebuilder:default=1800
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=86400
	RebootTimeoutSeconds int32 `json:"rebootTimeoutSeconds,omitempty"`
}

// UpgradeImageSource is a discriminated union of supported image
// lifecycle intents. URL and ConfigMapRef supply bytes for gNOI OS.Install;
// Preinstalled activates a version that is already in the device install
// inventory; DeviceFile asks the platform driver to register a file already
// resident on the device. LocalPath is retained only as a deprecated,
// preinstalled-only compatibility form.
//
// +kubebuilder:validation:XValidation:rule="(has(self.url) ? 1 : 0) + (has(self.configMapRef) ? 1 : 0) + (has(self.preinstalled) ? 1 : 0) + (has(self.deviceFile) ? 1 : 0) + (has(self.localPath) ? 1 : 0) == 1",message="exactly one of url, configMapRef, preinstalled, deviceFile, or localPath must be set"
// +kubebuilder:validation:XValidation:rule="has(self.url) == has(self.sha256)",message="sha256 must be set if and only if url is set"
// +kubebuilder:validation:XValidation:rule="!has(self.urlSecretRef) || has(self.url)",message="urlSecretRef may be set only when url is set"
// +kubebuilder:validation:XValidation:rule="!has(self.urlSecretRef) || self.urlSecretRef.name.size() > 0",message="urlSecretRef.name must not be empty"
// +kubebuilder:validation:XValidation:rule="!has(self.urlSecretRef) || self.url.matches('^(ftp|scp|sftp)://')",message="urlSecretRef is supported only for ftp, scp, or sftp URLs"
// +kubebuilder:validation:XValidation:rule="!has(self.url) || !self.url.matches('^(https?|tftp|ftp|scp|sftp)://[^/?#]*@')",message="url must not contain user information; use an endpoint-bound urlSecretRef"
// +kubebuilder:validation:XValidation:rule="!has(self.configMapRef) || self.configMapRef.name.size() > 0",message="configMapRef.name must not be empty"
// +kubebuilder:validation:XValidation:rule="!has(self.localPathSHA256) || has(self.localPath)",message="localPathSHA256 may be set only when localPath is set"
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
	// transfer credentials. To prevent credentials from being forwarded to an
	// endpoint chosen only by the upgrade author, the Secret must carry label
	// cisco.vk/purpose=software-image-source and data keys allowedScheme,
	// allowedHost, and allowedPort matching the URL's canonical endpoint. FTP
	// may use username/password; SCP and SFTP may use username/password or
	// username/privateKey/passphrase. SCP and SFTP host-key verification uses
	// knownHosts or known_hosts from this Secret unless the explicitly gated
	// insecureSkipHostKey option is enabled. URLs containing user information
	// are rejected; credentials must be supplied by an endpoint-bound Secret.
	// +optional
	URLSecretRef *corev1.LocalObjectReference `json:"urlSecretRef,omitempty"`

	// ConfigMapRef names a ConfigMap whose binaryData["image"] holds
	// the bytes. Capped at ~900 KiB by Kubernetes — testing only.
	// +optional
	ConfigMapRef *corev1.LocalObjectReference `json:"configMapRef,omitempty"`

	// Preinstalled declares that TargetVersion is already registered in the
	// device install inventory and should only be activated. The reconciler
	// fails closed when it cannot confirm one exact activatable inventory
	// entry; this intent never transfers or registers an image.
	// +optional
	Preinstalled *PreinstalledImageSource `json:"preinstalled,omitempty"`

	// DeviceFile identifies an image file already resident on the device.
	// The platform driver verifies its digest, registers it in the native
	// install inventory, and then hands the resulting exact version to the
	// generic gNOI activation lifecycle. This intent is supported only by
	// drivers that implement device-file registration.
	// +optional
	DeviceFile *DeviceFileImageSource `json:"deviceFile,omitempty"`

	// LocalPath is the deprecated compatibility form for a preinstalled
	// image. It may be used only when TargetVersion is already registered in
	// the device install inventory. The path is never registered or passed to
	// a platform install command; use DeviceFile for that workflow. Path must
	// be an absolute IOS-XE filesystem path.
	// Deprecated: use Preinstalled to activate an installed version, or
	// DeviceFile to register a file resident on the device.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^(flash|bootflash|harddisk):`
	LocalPath string `json:"localPath,omitempty"`

	// LocalPathSHA256 optionally verifies the legacy LocalPath file before
	// activation by reading it through gNOI File.Get. It does not cause the
	// file to be registered in the install inventory.
	// Deprecated: use DeviceFile.sha256 for device-file registration.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	LocalPathSHA256 string `json:"localPathSHA256,omitempty"`
}

// PreinstalledImageSource is an explicit marker for activation-only intent.
// TargetVersion on IOSXESoftwareUpgradeSpec identifies the installed version.
type PreinstalledImageSource struct{}

// DeviceFileImageSource identifies and authenticates an image already stored
// on the target device before the upgrade starts.
type DeviceFileImageSource struct {
	// Path is an absolute platform filesystem path to the image artifact.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=128
	// +kubebuilder:validation:Pattern=`^(flash|bootflash|harddisk):.+$`
	Path string `json:"path"`

	// SHA256 is the lowercase hex SHA-256 digest that must match the
	// device-resident file before any platform install operation is invoked.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	SHA256 string `json:"sha256"`
}

// UpgradeInventoryState is the normalized lifecycle state of TargetVersion
// in the device's native install inventory.
//
// +kubebuilder:validation:Enum=Absent;Present;Installed;ProvisionedUncommitted;ProvisionedCommitted;InProgress;Invalid;Unknown
type UpgradeInventoryState string

const (
	UpgradeInventoryStateAbsent                 UpgradeInventoryState = "Absent"
	UpgradeInventoryStatePresent                UpgradeInventoryState = "Present"
	UpgradeInventoryStateInstalled              UpgradeInventoryState = "Installed"
	UpgradeInventoryStateProvisionedUncommitted UpgradeInventoryState = "ProvisionedUncommitted"
	UpgradeInventoryStateProvisionedCommitted   UpgradeInventoryState = "ProvisionedCommitted"
	UpgradeInventoryStateInProgress             UpgradeInventoryState = "InProgress"
	UpgradeInventoryStateInvalid                UpgradeInventoryState = "Invalid"
	UpgradeInventoryStateUnknown                UpgradeInventoryState = "Unknown"
)

// UpgradeExecutionModel identifies the durable mutation-safety contract used
// by the reconciler that owns an upgrade workflow.
type UpgradeExecutionModel string

const (
	// UpgradeExecutionModelAtMostOnceV1 records that every mutation in this
	// workflow is protected by the v1 durable request markers.
	UpgradeExecutionModelAtMostOnceV1 UpgradeExecutionModel = "AtMostOnceV1"
)

// UpgradeWindow bounds when unclaimed lifecycle work may begin. The reconciler
// rechecks NotAfter before every device mutation that has not yet been claimed;
// it cannot cancel a mutation already submitted or in flight.
//
// +kubebuilder:validation:XValidation:rule="!has(self.notBefore) || !has(self.notAfter) || self.notBefore <= self.notAfter",message="notBefore must be earlier than or equal to notAfter"
type UpgradeWindow struct {
	// NotBefore is the earliest time the reconciler may begin lifecycle work.
	// +optional
	NotBefore *metav1.Time `json:"notBefore,omitempty"`
	// NotAfter is the latest time the reconciler may claim a new device mutation.
	// Past this, the CR transitions to a terminal phase with reason
	// MaintenanceWindowExpired unless a previously claimed mutation still needs
	// read-only observation.
	// +optional
	NotAfter *metav1.Time `json:"notAfter,omitempty"`
}

// IOSXESoftwareUpgradeStatus carries observed state.
type IOSXESoftwareUpgradeStatus struct {
	// Phase is the current state-machine position.
	// +optional
	Phase UpgradePhase `json:"phase,omitempty"`

	// ExecutionModel is persisted before this reconciler advances a new or
	// safely adoptable workflow. Its absence on a released in-flight phase is
	// treated as an unknown device outcome and is never replayed. Unknown
	// non-empty values are preserved and fenced for controller downgrade safety.
	// +optional
	ExecutionModel UpgradeExecutionModel `json:"executionModel,omitempty"`

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
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// TransferProgress reports cumulative bytes uploaded during the
	// Transferring phase. Preserved and marked complete after validation.
	// +optional
	TransferProgress *UpgradeTransferProgress `json:"transferProgress,omitempty"`

	// SourceDigest is the algorithm-qualified content address of the image used
	// by this operation (for example, "sha256:<64 lowercase hex characters>").
	// It is persisted before a transfer or device-file staging side effect.
	// +optional
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	SourceDigest string `json:"sourceDigest,omitempty"`

	// SourceSize is the verified byte count persisted with SourceDigest for URL
	// and ConfigMap transfers. It remains zero for device-resident sources.
	// +optional
	// +kubebuilder:validation:Minimum=0
	SourceSize int64 `json:"sourceSize,omitempty"`

	// StagingOperationID is the durable correlation key for a platform-native
	// device-file registration operation. It is persisted before submission;
	// after an ambiguous response the reconciler observes but never replays it.
	// +optional
	// +kubebuilder:validation:Format=uuid
	StagingOperationID string `json:"stagingOperationID,omitempty"`

	// StagingRequested is a durable at-most-once marker set before the native
	// device-file registration RPC is submitted.
	// +optional
	StagingRequested bool `json:"stagingRequested,omitempty"`

	// InventoryState is the normalized device install-inventory state most
	// recently observed for TargetVersion.
	// +optional
	InventoryState UpgradeInventoryState `json:"inventoryState,omitempty"`

	// RunningVersion is the device-reported running OS version after
	// Verifying.
	// +optional
	RunningVersion string `json:"runningVersion,omitempty"`

	// PreviousVersion is the device-reported OS version captured before
	// image activation. It is used as the rollback target when
	// RollbackOnFailure is enabled.
	// +optional
	PreviousVersion string `json:"previousVersion,omitempty"`

	// ValidatedVersion is the exact device-reported OS version returned by
	// gNOI OS.Install Validated or resolved from native install inventory.
	// IOS XE may require this exact value on OS.Activate even when the
	// requested TargetVersion is a shorter release prefix.
	// +optional
	ValidatedVersion string `json:"validatedVersion,omitempty"`

	// IndividualSupervisorInstall reports that gNOI requires the package to
	// be installed separately on each supervisor.
	// +optional
	IndividualSupervisorInstall bool `json:"individualSupervisorInstall,omitempty"`

	// PrimarySupervisorInstalled records successful installation on the
	// primary supervisor for restart-safe dual-supervisor retries.
	// +optional
	PrimarySupervisorInstalled bool `json:"primarySupervisorInstalled,omitempty"`

	// PrimarySupervisorInstallRequested is a durable at-most-once marker set
	// before opening OS.Install for the active/primary supervisor. It is also
	// used for single-supervisor devices.
	// +optional
	PrimarySupervisorInstallRequested bool `json:"primarySupervisorInstallRequested,omitempty"`

	// StandbySupervisorInstalled records successful installation on the
	// standby supervisor for restart-safe dual-supervisor retries.
	// +optional
	StandbySupervisorInstalled bool `json:"standbySupervisorInstalled,omitempty"`

	// StandbySupervisorInstallRequested is a durable at-most-once marker set
	// before opening OS.Install for the standby supervisor.
	// +optional
	StandbySupervisorInstallRequested bool `json:"standbySupervisorInstallRequested,omitempty"`

	// StandbySupervisorActivationRequested is a durable at-most-once marker set
	// before OS.Activate targets the standby supervisor.
	// +optional
	StandbySupervisorActivationRequested bool `json:"standbySupervisorActivationRequested,omitempty"`

	// StandbySupervisorActivated records that OS.Verify subsequently reported
	// the standby supervisor ready on TargetVersion.
	// +optional
	StandbySupervisorActivated bool `json:"standbySupervisorActivated,omitempty"`

	// PrimarySupervisorActivationRequested is a durable at-most-once marker set
	// before OS.Activate targets the active/primary supervisor. It is also used
	// for single-supervisor devices.
	// +optional
	PrimarySupervisorActivationRequested bool `json:"primarySupervisorActivationRequested,omitempty"`

	// NoRebootActivationAccepted records an affirmative OS.Activate response
	// for a NoReboot request so follow-up reachability checks can be retried
	// without replaying the mutation.
	// +optional
	NoRebootActivationAccepted bool `json:"noRebootActivationAccepted,omitempty"`

	// RollbackActivationRequested is a durable at-most-once marker set before
	// OS.Activate targets PreviousVersion.
	// +optional
	RollbackActivationRequested bool `json:"rollbackActivationRequested,omitempty"`

	// StartTime is when the reconciler first transitioned out of Pending.
	// +optional
	StartTime *metav1.Time `json:"startTime,omitempty"`

	// InstallStartTime is when the first image-install or device-file staging
	// mutation was durably recorded. InstallTimeoutSeconds is measured from it.
	// +optional
	InstallStartTime *metav1.Time `json:"installStartTime,omitempty"`

	// ActivationControlStartTime bounds initial activation-client recovery by
	// RebootTimeoutSeconds without consuming the subsequent reboot deadline.
	// It is recorded on the first client-acquisition failure before activation.
	// +optional
	ActivationControlStartTime *metav1.Time `json:"activationControlStartTime,omitempty"`

	// ActivationStartTime is when the first activation mutation was durably
	// recorded. RebootTimeoutSeconds is measured from it.
	// +optional
	ActivationStartTime *metav1.Time `json:"activationStartTime,omitempty"`

	// RollbackStartTime is when rollback recovery began. RebootTimeoutSeconds is
	// measured independently from it and includes pre-activation reachability.
	// +optional
	RollbackStartTime *metav1.Time `json:"rollbackStartTime,omitempty"`

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

	// RetryCount is retained for status wire compatibility with earlier
	// controllers. It is not incremented or used by the current reconciler.
	// Deprecated: retained for storage compatibility; ignored by the reconciler.
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
