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

// ManagedUpgradeProtocolVersion identifies the manager/worker handshake that
// gates every new device mutation for a campaign-created leaf.
//
// +kubebuilder:validation:Enum=rollout-v1
type ManagedUpgradeProtocolVersion string

const (
	ManagedUpgradeProtocolRolloutV1 ManagedUpgradeProtocolVersion = "rollout-v1"
)

// UpgradeManagerAdmissionState is the manager-owned mutation grant state.
// Missing admission means denied for a managed device. It remains optional on
// standalone legacy leaves, whose execution mode is selected outside this API.
//
// +kubebuilder:validation:Enum=Pending;Granted;Revoked;Settled
type UpgradeManagerAdmissionState string

const (
	UpgradeManagerAdmissionPending UpgradeManagerAdmissionState = "Pending"
	UpgradeManagerAdmissionGranted UpgradeManagerAdmissionState = "Granted"
	UpgradeManagerAdmissionRevoked UpgradeManagerAdmissionState = "Revoked"
	UpgradeManagerAdmissionSettled UpgradeManagerAdmissionState = "Settled"
)

// UpgradeManagerAdmissionStatus is written only by the manager and protected
// by native admission. A worker may claim a device mutation only from Granted
// after re-reading this exact leaf and committing its durable claim against the
// same resourceVersion.
//
// +kubebuilder:validation:XValidation:rule="self.state != 'Granted' || (has(self.protocolVersion) && has(self.campaignUID) && has(self.planHash) && has(self.policyUID) && has(self.policyResourceVersion) && self.policyEpoch >= 1 && has(self.ledgerUID) && has(self.reservationID) && has(self.topologyLockID) && has(self.leafUID) && has(self.deviceUID) && has(self.physicalIdentity) && has(self.nodeUID) && has(self.controlRevision))",message="a granted admission requires the complete protocol, policy epoch, topology-lock acquisition, and identity binding"
// +kubebuilder:validation:XValidation:rule="self.state != 'Pending' || has(self.topologyLockID)",message="a pending admission requires its topology-lock acquisition identity"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.campaignUID) || self.campaignUID == oldSelf.campaignUID",message="campaignUID is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.planHash) || self.planHash == oldSelf.planHash",message="planHash is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.policyUID) || self.policyUID == oldSelf.policyUID",message="policyUID is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.policyResourceVersion) || self.policyResourceVersion == oldSelf.policyResourceVersion || (oldSelf.state == 'Revoked' && has(oldSelf.revocationReason) && oldSelf.revocationReason == 'PolicyEpochTransition' && self.state == 'Pending' && self.policyEpoch > oldSelf.policyEpoch)",message="policyResourceVersion changes only when a policy-epoch revocation is rearmed"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.ledgerUID) || self.ledgerUID == oldSelf.ledgerUID",message="ledgerUID is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.reservationID) || self.reservationID == oldSelf.reservationID",message="reservationID is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.topologyLockID) || self.topologyLockID == oldSelf.topologyLockID || (oldSelf.state == 'Revoked' && has(oldSelf.revocationReason) && oldSelf.revocationReason == 'PolicyEpochTransition' && self.state == 'Pending' && self.policyEpoch > oldSelf.policyEpoch)",message="topologyLockID changes only when a policy-epoch revocation is rearmed"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.leafUID) || self.leafUID == oldSelf.leafUID",message="leafUID is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.deviceUID) || self.deviceUID == oldSelf.deviceUID",message="deviceUID is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.physicalIdentity) || self.physicalIdentity == oldSelf.physicalIdentity",message="physicalIdentity is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.nodeUID) || self.nodeUID == oldSelf.nodeUID",message="nodeUID is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.protocolVersion) || self.protocolVersion == oldSelf.protocolVersion",message="protocolVersion is immutable once set"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.controlRevision) || (has(self.controlRevision) && self.controlRevision >= oldSelf.controlRevision)",message="admission controlRevision cannot decrease or be removed"
// +kubebuilder:validation:XValidation:rule="self.policyEpoch >= oldSelf.policyEpoch",message="policy epoch cannot decrease"
// +kubebuilder:validation:XValidation:rule="oldSelf.state == 'Pending' ? self.state in ['Pending', 'Granted', 'Revoked'] : (oldSelf.state == 'Granted' ? self.state in ['Granted', 'Revoked', 'Settled'] : (oldSelf.state == 'Revoked' ? (self.state in ['Revoked', 'Settled'] || (self.state == 'Pending' && has(oldSelf.revocationReason) && oldSelf.revocationReason == 'PolicyEpochTransition' && self.policyEpoch > oldSelf.policyEpoch)) : self.state == 'Settled'))",message="manager admission state cannot regress except a newer policy epoch may rearm a policy-transition revocation"
// +kubebuilder:validation:XValidation:rule="self.state == 'Revoked' ? has(self.revocationReason) : !has(self.revocationReason)",message="revoked admission requires a reason and non-revoked admission must not retain one"
type UpgradeManagerAdmissionStatus struct {
	// ProtocolVersion must match the current manager/worker handshake.
	// +kubebuilder:validation:Optional
	ProtocolVersion ManagedUpgradeProtocolVersion `json:"protocolVersion,omitempty"`

	// State is Pending until ledger identity is bound, Granted while new claims
	// are authorized, Revoked when future claims are fenced, and Settled only
	// after accepted work and health evidence are resolved.
	// +kubebuilder:validation:Required
	State UpgradeManagerAdmissionState `json:"state"`

	// CampaignUID identifies the retained IOSXESoftwareRollout incarnation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	CampaignUID string `json:"campaignUID,omitempty"`

	// PlanHash binds this leaf to the approved frozen campaign plan.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	PlanHash string `json:"planHash,omitempty"`

	// PolicyUID binds the grant to the administrator policy incarnation covered
	// by PlanHash.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	PolicyUID string `json:"policyUID,omitempty"`

	// PolicyResourceVersion is the approved effective policy version. A newer
	// current policy may only tighten this grant before a claim.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	PolicyResourceVersion string `json:"policyResourceVersion,omitempty"`

	// PolicyEpoch is the durable rollout safety epoch. A worker acknowledgement
	// and every mutation claim must match it exactly.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	PolicyEpoch int64 `json:"policyEpoch"`

	// RevocationReason determines whether an unclaimed retained leaf may ever be
	// rearmed. Only PolicyEpochTransition is reversible.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Enum=PolicyEpochTransition;AdministratorPolicyChanged;SourceIdentityChanged;CampaignCancelled;TargetIdentityChanged;CampaignTargetFailed
	RevocationReason string `json:"revocationReason,omitempty"`

	// LedgerUID freezes the single authoritative reservation-ledger
	// incarnation. A missing or recreated ledger freezes, rather than resets,
	// admission.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	LedgerUID string `json:"ledgerUID,omitempty"`

	// ReservationID identifies the atomic device and domain reservation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9._:-]*$`
	ReservationID string `json:"reservationID,omitempty"`

	// TopologyLockID is the exact manager acquisition that backs this
	// reservation. Retaining it after ledger removal makes settlement and
	// cleanup replayable across a crash without allowing an older cleanup to
	// target a later acquisition that reuses ReservationID.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{32}$`
	TopologyLockID string `json:"topologyLockID,omitempty"`

	// LeafUID prevents adoption of a recreated same-name child.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	LeafUID string `json:"leafUID,omitempty"`

	// DeviceUID is the planned CiscoDevice incarnation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	DeviceUID string `json:"deviceUID,omitempty"`

	// DeviceGeneration is the planned CiscoDevice generation. The worker
	// re-reads the object immediately before every new mutation claim so an
	// address, driver, port, trust-reference, or other spec change cannot inherit
	// an older campaign grant.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	DeviceGeneration int64 `json:"deviceGeneration"`

	// PhysicalIdentity is the verified stable serial or equivalent identity
	// deduplicated by the reservation ledger.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	PhysicalIdentity string `json:"physicalIdentity,omitempty"`

	// NodeUID is the planned identity-bound Node incarnation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	NodeUID string `json:"nodeUID,omitempty"`

	// ControlRevision is the minimum manager control revision a worker must
	// observe before claiming a new mutation.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	ControlRevision *int64 `json:"controlRevision,omitempty"`

	// UpdatedAt is the manager admission transition time.
	// +kubebuilder:validation:Required
	UpdatedAt metav1.Time `json:"updatedAt"`
}

// UpgradeManagerControlStatus carries manager-owned leaf pause/cancel intent.
// A larger revision is required for every change and cancellation is terminal.
//
// +kubebuilder:validation:XValidation:rule="self.revision >= oldSelf.revision",message="manager control revision cannot decrease"
// +kubebuilder:validation:XValidation:rule="self == oldSelf || self.revision > oldSelf.revision",message="a manager control change requires a larger revision"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.cancel) || !oldSelf.cancel || (has(self.cancel) && self.cancel)",message="managed leaf cancellation is terminal"
type UpgradeManagerControlStatus struct {
	// Revision is copied from the campaign control revision whose effect this
	// leaf must observe.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	Revision int64 `json:"revision"`

	// Pause blocks new unclaimed mutations but cannot abort accepted work.
	Pause bool `json:"pause,omitempty"`

	// Cancel permanently blocks future claims for this leaf.
	Cancel bool `json:"cancel,omitempty"`

	// UpdatedAt is the manager control transition time.
	// +kubebuilder:validation:Required
	UpdatedAt metav1.Time `json:"updatedAt"`

	// Reason is a bounded machine-readable control reason.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	Reason string `json:"reason,omitempty"`
}

// UpgradeWorkerControlState reports the effective claim fence observed by the
// worker, separately from manager-requested control.
//
// +kubebuilder:validation:Enum=Denied;Ready;Paused;Cancelled;Claimed;Settled
type UpgradeWorkerControlState string

const (
	UpgradeWorkerControlDenied    UpgradeWorkerControlState = "Denied"
	UpgradeWorkerControlReady     UpgradeWorkerControlState = "Ready"
	UpgradeWorkerControlPaused    UpgradeWorkerControlState = "Paused"
	UpgradeWorkerControlCancelled UpgradeWorkerControlState = "Cancelled"
	UpgradeWorkerControlClaimed   UpgradeWorkerControlState = "Claimed"
	UpgradeWorkerControlSettled   UpgradeWorkerControlState = "Settled"
)

// UpgradeWorkerControlStatus is the worker-owned acknowledgement of the
// manager grant and control revision.
type UpgradeWorkerControlStatus struct {
	// ObservedAdmissionState is the manager admission state read by the worker.
	// +kubebuilder:validation:Required
	ObservedAdmissionState UpgradeManagerAdmissionState `json:"observedAdmissionState"`

	// ObservedPolicyEpoch is the exact manager admission epoch acknowledged by
	// this worker state.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	ObservedPolicyEpoch int64 `json:"observedPolicyEpoch"`

	// ObservedControlRevision is the manager revision incorporated into the
	// worker's effective claim fence.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	ObservedControlRevision int64 `json:"observedControlRevision"`

	// ObservedWorkerConfigRevision is the running worker revision that
	// acknowledged this admission. The rollout manager compares it with the
	// CiscoDevice's current live worker proof before granting; it is deliberately
	// not frozen in managerAdmission so an in-place Secret rotation can converge.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	ObservedWorkerConfigRevision string `json:"observedWorkerConfigRevision"`

	// EffectiveState exposes whether pause/cancellation is effective or an
	// earlier claim must still be observed.
	// +kubebuilder:validation:Required
	EffectiveState UpgradeWorkerControlState `json:"effectiveState"`

	// UpdatedAt is the last effective-state observation time.
	// +kubebuilder:validation:Required
	UpdatedAt metav1.Time `json:"updatedAt"`

	// Message is a bounded worker acknowledgement summary.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	Message string `json:"message,omitempty"`
}

// ManagedDrainProtocolVersion identifies an explicitly enabled workload-drain
// handshake. It is separate from ManagedUpgradeProtocolVersion so an older
// rollout-v1 worker cannot accidentally interpret drain authority as mutation
// authority.
//
// +kubebuilder:validation:Enum=pdb-drain-v1
type ManagedDrainProtocolVersion string

const (
	ManagedDrainProtocolPDBV1 ManagedDrainProtocolVersion = "pdb-drain-v1"
)

// UpgradeManagerDrainState is the manager-owned state of a bounded drain.
// Recovery is terminal with respect to new evictions: it may only reconcile
// work already accepted and restore workload/scheduling invariants.
//
// +kubebuilder:validation:Enum=Preparing;Guarded;Evicting;Drained;Promoting;Promoted;Recovering;Settled
type UpgradeManagerDrainState string

const (
	UpgradeManagerDrainPreparing  UpgradeManagerDrainState = "Preparing"
	UpgradeManagerDrainGuarded    UpgradeManagerDrainState = "Guarded"
	UpgradeManagerDrainEvicting   UpgradeManagerDrainState = "Evicting"
	UpgradeManagerDrainDrained    UpgradeManagerDrainState = "Drained"
	UpgradeManagerDrainPromoting  UpgradeManagerDrainState = "Promoting"
	UpgradeManagerDrainPromoted   UpgradeManagerDrainState = "Promoted"
	UpgradeManagerDrainRecovering UpgradeManagerDrainState = "Recovering"
	UpgradeManagerDrainSettled    UpgradeManagerDrainState = "Settled"
)

// UpgradeDrainPodPhase records the durable progress of one exact selected Pod.
// EvictionRequested is written only after policy/v1 Eviction was accepted;
// this makes it a durable alternative to observing deletionTimestamp during a
// provider deletion callback.
//
// +kubebuilder:validation:Enum=Selected;Protected;EvictionRequested;TerminationObserved;DeviceClean;Released;Complete
type UpgradeDrainPodPhase string

const (
	UpgradeDrainPodSelected            UpgradeDrainPodPhase = "Selected"
	UpgradeDrainPodProtected           UpgradeDrainPodPhase = "Protected"
	UpgradeDrainPodEvictionRequested   UpgradeDrainPodPhase = "EvictionRequested"
	UpgradeDrainPodTerminationObserved UpgradeDrainPodPhase = "TerminationObserved"
	UpgradeDrainPodDeviceClean         UpgradeDrainPodPhase = "DeviceClean"
	UpgradeDrainPodReleased            UpgradeDrainPodPhase = "Released"
	UpgradeDrainPodComplete            UpgradeDrainPodPhase = "Complete"
)

// UpgradeDrainObjectReference binds drain eligibility to one exact namespaced
// controller or PodDisruptionBudget incarnation.
type UpgradeDrainObjectReference struct {
	// APIVersion and Kind identify the native Kubernetes object type.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	APIVersion string `json:"apiVersion"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	Kind string `json:"kind"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// UID prevents a deleted object from being replaced under the same name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`

	// Generation freezes the eligibility inputs read from this object.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	Generation int64 `json:"generation"`
}

// UpgradeDrainPDBStatus captures the policy/v1 PodDisruptionBudget evidence
// used to select a Pod. Keeping the health arithmetic explicit makes the
// decision auditable without trusting an opaque eligibility hash.
//
// +kubebuilder:validation:XValidation:rule="self.observedGeneration == self.generation",message="drain PDB status must have observed its exact metadata generation"
// +kubebuilder:validation:XValidation:rule="self.currentHealthy >= self.desiredHealthy && self.expectedPods >= self.currentHealthy && self.expectedPods >= self.desiredHealthy",message="drain PDB health evidence is inconsistent"
type UpgradeDrainPDBStatus struct {
	UpgradeDrainObjectReference `json:",inline"`

	// ObservedGeneration is status.observedGeneration from the exact PDB.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	ObservedGeneration int64 `json:"observedGeneration"`

	// DisruptionsAllowed must be positive in the frozen eligibility snapshot;
	// the eviction subresource still performs the authoritative live check.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000000
	DisruptionsAllowed int32 `json:"disruptionsAllowed"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000000
	CurrentHealthy int32 `json:"currentHealthy"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1000000
	DesiredHealthy int32 `json:"desiredHealthy"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=1000000
	ExpectedPods int32 `json:"expectedPods"`
}

// UpgradeDrainPodStatus is the bounded, identity-frozen manager record for one
// drain candidate. A provider may act only on the exact UID and session in this
// record; names alone never authorize device-side deletion.
//
// +kubebuilder:validation:XValidation:rule="oldSelf.phase == 'Selected' ? self.phase in ['Selected', 'Protected', 'Complete'] : (oldSelf.phase == 'Protected' ? self.phase in ['Protected', 'EvictionRequested', 'TerminationObserved', 'Released'] : (oldSelf.phase == 'EvictionRequested' ? self.phase in ['EvictionRequested', 'TerminationObserved'] : (oldSelf.phase == 'TerminationObserved' ? self.phase in ['TerminationObserved', 'DeviceClean'] : (oldSelf.phase == 'DeviceClean' ? self.phase in ['DeviceClean', 'Released'] : (oldSelf.phase == 'Released' ? self.phase in ['Released', 'Complete'] : self.phase == 'Complete')))))",message="drain Pod phase cannot regress or skip accepted-teardown cleanup evidence"
// +kubebuilder:validation:XValidation:rule="self.eligibilityHash == oldSelf.eligibilityHash",message="drain Pod eligibility snapshot is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.protectedAt) || (has(self.protectedAt) && self.protectedAt == oldSelf.protectedAt)",message="protectedAt is append-only and immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.evictionRequestedAt) || (has(self.evictionRequestedAt) && self.evictionRequestedAt == oldSelf.evictionRequestedAt)",message="evictionRequestedAt is append-only and immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.deletionObservedAt) || (has(self.deletionObservedAt) && self.deletionObservedAt == oldSelf.deletionObservedAt)",message="deletionObservedAt is append-only and immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.deletionObservedInventoryRevision) || (has(self.deletionObservedInventoryRevision) && self.deletionObservedInventoryRevision == oldSelf.deletionObservedInventoryRevision)",message="deletionObservedInventoryRevision is append-only and immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.deviceCleanAt) || (has(self.deviceCleanAt) && self.deviceCleanAt == oldSelf.deviceCleanAt)",message="deviceCleanAt is append-only and immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.releasedAt) || (has(self.releasedAt) && self.releasedAt == oldSelf.releasedAt)",message="releasedAt is append-only and immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.evictionRequestedAt) || (has(self.protectedAt) && self.evictionRequestedAt >= self.protectedAt)",message="evictionRequestedAt cannot precede protectedAt"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionObservedAt) || (has(self.evictionRequestedAt) && self.deletionObservedAt >= self.evictionRequestedAt)",message="deletionObservedAt cannot precede evictionRequestedAt"
// +kubebuilder:validation:XValidation:rule="!has(self.deletionObservedInventoryRevision) || has(self.deletionObservedAt)",message="deletionObservedInventoryRevision requires termination evidence"
// +kubebuilder:validation:XValidation:rule="!has(self.deviceCleanAt) || (has(self.deletionObservedAt) && self.deviceCleanAt >= self.deletionObservedAt && self.deviceCleanInventoryRevision > (has(self.deletionObservedInventoryRevision) ? self.deletionObservedInventoryRevision : 0))",message="deviceCleanAt requires inventory evidence newer than the termination baseline"
// +kubebuilder:validation:XValidation:rule="!has(self.releasedAt) || (!has(self.deviceCleanAt) || self.releasedAt >= self.deviceCleanAt)",message="releasedAt cannot precede deviceCleanAt"
// +kubebuilder:validation:XValidation:rule="self.phase in ['Selected', 'Complete'] || has(self.protectedAt)",message="protected and teardown phases require protectedAt"
// +kubebuilder:validation:XValidation:rule="!(self.phase in ['EvictionRequested', 'TerminationObserved', 'DeviceClean']) || has(self.evictionRequestedAt)",message="accepted eviction phases require evictionRequestedAt"
// +kubebuilder:validation:XValidation:rule="!(self.phase in ['TerminationObserved', 'DeviceClean']) || has(self.deletionObservedAt)",message="termination evidence phases require deletionObservedAt"
// +kubebuilder:validation:XValidation:rule="self.phase != 'DeviceClean' || (has(self.deviceCleanAt) && self.deviceCleanInventoryRevision > 0)",message="DeviceClean requires timestamped positive inventory evidence"
// +kubebuilder:validation:XValidation:rule="self.phase != 'Released' || has(self.releasedAt)",message="Released requires releasedAt"
type UpgradeDrainPodStatus struct {
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`

	// EligibilityHash is the canonical digest of the Pod identity, controller
	// chain, exact PDB evidence, and bounded termination policy. Both manager and
	// worker recompute it before acting, making nested snapshot drift fail closed
	// without prohibitively expensive recursive CEL transition rules.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=71
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	EligibilityHash string `json:"eligibilityHash"`

	// Controller is the Pod's exact controlling owner.
	// +kubebuilder:validation:Required
	Controller UpgradeDrainObjectReference `json:"controller"`

	// WorkloadController is the exact top-level native workload controller when
	// it differs from Controller (for example Deployment above ReplicaSet).
	// +kubebuilder:validation:Optional
	WorkloadController *UpgradeDrainObjectReference `json:"workloadController,omitempty"`

	// PDBs contains the one exact policy/v1 PodDisruptionBudget that selected
	// this Pod when eligibility was frozen. Kubernetes rejects Eviction when
	// more than one PDB selects the same Pod.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=1
	// +listType=map
	// +listMapKey=uid
	PDBs []UpgradeDrainPDBStatus `json:"pdbs"`

	// TerminationGracePeriodSeconds is the campaign-capped grace sent with the
	// Eviction request.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=600
	TerminationGracePeriodSeconds int64 `json:"terminationGracePeriodSeconds"`

	// +kubebuilder:validation:Required
	Phase UpgradeDrainPodPhase `json:"phase"`

	// The following timestamps are append-only audit evidence for the bounded
	// drain state machine.
	// +kubebuilder:validation:Optional
	ProtectedAt *metav1.Time `json:"protectedAt,omitempty"`
	// +kubebuilder:validation:Optional
	EvictionRequestedAt *metav1.Time `json:"evictionRequestedAt,omitempty"`
	// +kubebuilder:validation:Optional
	DeletionObservedAt *metav1.Time `json:"deletionObservedAt,omitempty"`
	// DeletionObservedInventoryRevision is the latest worker inventory revision
	// atomically observed when the manager first persisted termination. Only a
	// strictly newer scan can prove this particular Pod clean.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=0
	DeletionObservedInventoryRevision int64 `json:"deletionObservedInventoryRevision,omitempty"`
	// +kubebuilder:validation:Optional
	DeviceCleanAt *metav1.Time `json:"deviceCleanAt,omitempty"`
	// +kubebuilder:validation:Optional
	ReleasedAt *metav1.Time `json:"releasedAt,omitempty"`

	// DeviceCleanInventoryRevision is the positive worker inventory revision
	// that proved the exact Pod UID absent from the device.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=1
	DeviceCleanInventoryRevision int64 `json:"deviceCleanInventoryRevision,omitempty"`
}

// UpgradeManagerDrainStatus is the manager-owned, bounded drain authority.
// Its omission preserves the existing BlockIfRunning path and grants no
// workload-deletion authority.
//
// +kubebuilder:validation:XValidation:rule="self.protocolVersion == oldSelf.protocolVersion && self.sessionToken == oldSelf.sessionToken && self.reservationID == oldSelf.reservationID && self.policyEpoch == oldSelf.policyEpoch && self.nodeUID == oldSelf.nodeUID && self.nodeUnschedulableBefore == oldSelf.nodeUnschedulableBefore && self.maintenanceTaintPresentBefore == oldSelf.maintenanceTaintPresentBefore && self.startedAt == oldSelf.startedAt && self.drainDeadline == oldSelf.drainDeadline",message="manager drain identity and pre-guard evidence are immutable"
// +kubebuilder:validation:XValidation:rule="self.controlRevision >= oldSelf.controlRevision",message="manager drain controlRevision cannot decrease"
// +kubebuilder:validation:XValidation:rule="oldSelf.state == 'Preparing' ? self.state in ['Preparing', 'Guarded', 'Recovering'] : (oldSelf.state == 'Guarded' ? self.state in ['Guarded', 'Evicting', 'Recovering'] : (oldSelf.state == 'Evicting' ? self.state in ['Evicting', 'Drained', 'Recovering'] : (oldSelf.state == 'Drained' ? self.state in ['Drained', 'Promoting', 'Recovering'] : (oldSelf.state == 'Promoting' ? self.state in ['Promoting', 'Promoted', 'Recovering'] : (oldSelf.state == 'Promoted' ? self.state in ['Promoted', 'Recovering'] : (oldSelf.state == 'Recovering' ? self.state in ['Recovering', 'Settled'] : self.state == 'Settled'))))))",message="manager drain state cannot regress or bypass recovery"
// +kubebuilder:validation:XValidation:rule="self.drainDeadline > self.startedAt && self.updatedAt >= self.startedAt",message="manager drain deadline must follow start and updates cannot precede start"
// +kubebuilder:validation:XValidation:rule="self.updatedAt >= oldSelf.updatedAt",message="manager drain updatedAt cannot regress"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.recoveryDeadline) || (has(self.recoveryDeadline) && self.recoveryDeadline >= oldSelf.recoveryDeadline && (self.recoveryDeadline == oldSelf.recoveryDeadline || (oldSelf.state == 'Recovering' && self.state == 'Recovering' && self.controlRevision > oldSelf.controlRevision && self.updatedAt > oldSelf.updatedAt)))",message="recoveryDeadline is append-only and may advance only in Recovering with a newer audited control revision"
// +kubebuilder:validation:XValidation:rule="self.state in ['Recovering', 'Settled'] ? (has(self.recoveryDeadline) && self.recoveryDeadline > self.startedAt) : !has(self.recoveryDeadline)",message="recoveryDeadline is present only for Recovering or Settled and must follow start"
type UpgradeManagerDrainStatus struct {
	// +kubebuilder:validation:Required
	ProtocolVersion ManagedDrainProtocolVersion `json:"protocolVersion"`

	// +kubebuilder:validation:Required
	State UpgradeManagerDrainState `json:"state"`

	// SessionToken is a canonical UUID shared only by this drain, the exact
	// maintenance session/Lease, and protected Pods.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	SessionToken string `json:"sessionToken"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	ReservationID string `json:"reservationID"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	PolicyEpoch int64 `json:"policyEpoch"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	ControlRevision int64 `json:"controlRevision"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	NodeUID string `json:"nodeUID"`

	// NodeUnschedulableBefore records operator-owned scheduling state before
	// this exact session applied its guard. Recovery may clear unschedulable only
	// when this is false and the Node still carries this session's ownership.
	// +kubebuilder:validation:Required
	NodeUnschedulableBefore bool `json:"nodeUnschedulableBefore"`

	// MaintenanceTaintPresentBefore prevents recovery from removing a taint that
	// predated this drain. A false value is not sufficient by itself: the
	// session ownership annotation must still match before removal.
	// +kubebuilder:validation:Required
	MaintenanceTaintPresentBefore bool `json:"maintenanceTaintPresentBefore"`

	// +kubebuilder:validation:Required
	StartedAt metav1.Time `json:"startedAt"`

	// DrainDeadline is the immutable deadline for accepting new evictions.
	// +kubebuilder:validation:Required
	DrainDeadline metav1.Time `json:"drainDeadline"`

	// RecoveryDeadline bounds cleanup after normal drain progress stops. If
	// recovery outlives this window, the manager may extend it by one bounded
	// window only while Recovering and only with a strictly newer campaign
	// control revision. Renewal never authorizes a new eviction or disruptive
	// software mutation; it can resume only already-accepted teardown and fresh
	// device inventory needed to prove recovery.
	// +kubebuilder:validation:Optional
	RecoveryDeadline *metav1.Time `json:"recoveryDeadline,omitempty"`

	// +kubebuilder:validation:Required
	UpdatedAt metav1.Time `json:"updatedAt"`

	// Pods is a bounded UID-keyed snapshot; the configured campaign maxPods may
	// tighten this absolute API limit.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=32
	// +listType=map
	// +listMapKey=uid
	Pods []UpgradeDrainPodStatus `json:"pods,omitempty"`
}

// UpgradeWorkerDrainStatus is the worker-owned inventory acknowledgement for
// the exact manager drain session. It conveys observations only and is never
// authority to evict another Pod.
//
// +kubebuilder:validation:XValidation:rule="self.protocolVersion == oldSelf.protocolVersion && self.observedSessionToken == oldSelf.observedSessionToken && self.observedPolicyEpoch == oldSelf.observedPolicyEpoch",message="worker drain session identity is immutable"
// +kubebuilder:validation:XValidation:rule="self.observedControlRevision >= oldSelf.observedControlRevision && self.inventoryRevision >= oldSelf.inventoryRevision",message="worker drain revisions cannot decrease"
// +kubebuilder:validation:XValidation:rule="self.inventoryObservedAt >= oldSelf.inventoryObservedAt && self.updatedAt >= oldSelf.updatedAt",message="worker drain observation timestamps cannot regress"
// +kubebuilder:validation:XValidation:rule="self.inventoryRevision == oldSelf.inventoryRevision || (self.inventoryRevision > oldSelf.inventoryRevision && self.inventoryObservedAt > oldSelf.inventoryObservedAt && self.updatedAt > oldSelf.updatedAt)",message="a new inventory revision requires newer observation timestamps"
// +kubebuilder:validation:XValidation:rule="self.inventoryRevision > oldSelf.inventoryRevision || self == oldSelf",message="worker drain inventory evidence may change only with a strictly newer inventory revision"
// +kubebuilder:validation:XValidation:rule="self.observedWorkerConfigRevision == oldSelf.observedWorkerConfigRevision || (self.inventoryRevision > oldSelf.inventoryRevision && self.inventoryObservedAt > oldSelf.inventoryObservedAt && self.updatedAt > oldSelf.updatedAt)",message="worker configuration revision may change only with a strictly newer inventory observation"
type UpgradeWorkerDrainStatus struct {
	// +kubebuilder:validation:Required
	ProtocolVersion ManagedDrainProtocolVersion `json:"protocolVersion"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`
	ObservedSessionToken string `json:"observedSessionToken"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	ObservedPolicyEpoch int64 `json:"observedPolicyEpoch"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	ObservedControlRevision int64 `json:"observedControlRevision"`

	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	// A worker rotation makes the prior inventory stale; this value may advance
	// only together with a strictly newer device scan.
	ObservedWorkerConfigRevision string `json:"observedWorkerConfigRevision"`

	// ObservedWorkerPodUID binds inventory to the app worker process, including
	// restarts that keep the same configuration. Required for split workers.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	ObservedWorkerPodUID string `json:"observedWorkerPodUID,omitempty"`

	// InventoryRevision identifies one complete, device-derived workload scan.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	InventoryRevision int64 `json:"inventoryRevision"`

	// +kubebuilder:validation:Required
	InventoryObservedAt metav1.Time `json:"inventoryObservedAt"`

	// InventoryComplete is false whenever enumeration was partial or ambiguous.
	InventoryComplete bool `json:"inventoryComplete"`

	// RemainingAuthorizedPodUIDs is the bounded set of selected Pod UIDs still
	// observed on the device.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:XValidation:rule="self.all(uid, uid.size() >= 1 && uid.size() <= 128)",message="remaining authorized Pod UIDs must contain 1-128 characters"
	// +listType=set
	RemainingAuthorizedPodUIDs []string `json:"remainingAuthorizedPodUIDs,omitempty"`

	// ForeignDeviceWorkloadCount and UnknownDeviceWorkloadCount keep incomplete
	// attribution fail-closed without persisting an unbounded inventory.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2147483647
	ForeignDeviceWorkloadCount int32 `json:"foreignDeviceWorkloadCount,omitempty"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=2147483647
	UnknownDeviceWorkloadCount int32 `json:"unknownDeviceWorkloadCount,omitempty"`

	// +kubebuilder:validation:Required
	UpdatedAt metav1.Time `json:"updatedAt"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=128
	Reason string `json:"reason,omitempty"`

	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=256
	Message string `json:"message,omitempty"`
}

// UpgradeManagedMutationStage names each durable device-mutating claim.
//
// +kubebuilder:validation:Enum=Staging;PrimaryInstall;StandbyInstall;StandbyActivation;PrimaryActivation;RollbackActivation
type UpgradeManagedMutationStage string

const (
	UpgradeManagedMutationStaging            UpgradeManagedMutationStage = "Staging"
	UpgradeManagedMutationPrimaryInstall     UpgradeManagedMutationStage = "PrimaryInstall"
	UpgradeManagedMutationStandbyInstall     UpgradeManagedMutationStage = "StandbyInstall"
	UpgradeManagedMutationStandbyActivation  UpgradeManagedMutationStage = "StandbyActivation"
	UpgradeManagedMutationPrimaryActivation  UpgradeManagedMutationStage = "PrimaryActivation"
	UpgradeManagedMutationRollbackActivation UpgradeManagedMutationStage = "RollbackActivation"
)

// UpgradeManagedMutationClaimStatus binds a durable worker mutation claim to
// the exact reservation and manager-control revision it won against.
//
// +kubebuilder:validation:XValidation:rule="self.reservationID == oldSelf.reservationID && self.policyEpoch == oldSelf.policyEpoch && self.controlRevision == oldSelf.controlRevision && self.claimedAt == oldSelf.claimedAt",message="managed mutation claim identity is immutable"
type UpgradeManagedMutationClaimStatus struct {
	// Stage is the list-map key and claimed device mutation.
	// +kubebuilder:validation:Required
	Stage UpgradeManagedMutationStage `json:"stage"`

	// ReservationID must equal managerAdmission.reservationID.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9][A-Za-z0-9._:-]*$`
	ReservationID string `json:"reservationID"`

	// PolicyEpoch must exactly match the granted manager admission won by this
	// durable claim.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	PolicyEpoch int64 `json:"policyEpoch"`

	// ControlRevision is committed in the same resourceVersion update as the
	// existing durable stage-request marker.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=0
	ControlRevision int64 `json:"controlRevision"`

	// ClaimedAt is persisted before dispatch and never rewritten on recovery.
	// +kubebuilder:validation:Required
	ClaimedAt metav1.Time `json:"claimedAt"`
}

// IOSXESoftwareUpgradeStatus carries observed state.
//
// Once published, drain snapshots and individual manager-selected Pod records
// cannot be removed or added. The manager publishes the complete immutable
// candidate snapshot atomically with ManagerDrain; subsequent updates carry
// only state-machine and observation progress.
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.managerDrain) || has(self.managerDrain)",message="managerDrain cannot be removed once published"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.workerDrain) || has(self.workerDrain)",message="workerDrain cannot be removed once published"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.managerDrain) || !has(oldSelf.managerDrain.pods) || (has(self.managerDrain) && has(self.managerDrain.pods) && oldSelf.managerDrain.pods.all(p, self.managerDrain.pods.exists(n, n.uid == p.uid)))",message="managerDrain Pod entries cannot be removed once published"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.managerDrain) || !has(self.managerDrain) || !has(self.managerDrain.pods) || (has(oldSelf.managerDrain.pods) && self.managerDrain.pods.all(p, oldSelf.managerDrain.pods.exists(o, o.uid == p.uid)))",message="managerDrain Pod entries cannot be added after publication"
// +kubebuilder:validation:XValidation:rule="!has(self.workerDrain) || (has(self.managerDrain) && self.workerDrain.protocolVersion == self.managerDrain.protocolVersion && self.workerDrain.observedSessionToken == self.managerDrain.sessionToken && self.workerDrain.observedPolicyEpoch == self.managerDrain.policyEpoch && self.workerDrain.observedControlRevision <= self.managerDrain.controlRevision)",message="workerDrain must bind the current manager drain session and may only lag its control revision"
// +kubebuilder:validation:XValidation:rule="!has(self.workerDrain) || !has(self.workerDrain.remainingAuthorizedPodUIDs) || (has(self.managerDrain) && has(self.managerDrain.pods) && self.workerDrain.remainingAuthorizedPodUIDs.all(uid, self.managerDrain.pods.exists(p, p.uid == uid)))",message="workerDrain remaining Pod UIDs must be a subset of the frozen manager snapshot"
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

	// ManagerAdmission is the manager-owned, identity-bound mutation grant for
	// a campaign-created leaf. On a managed device, absence is denial. Native
	// admission must prevent workers and ordinary editors from changing it.
	// +kubebuilder:validation:Optional
	ManagerAdmission *UpgradeManagerAdmissionStatus `json:"managerAdmission,omitempty"`

	// ManagerControl is manager-owned pause/cancel intent. A claim and a
	// revocation compete through resourceVersion on this same leaf object.
	// +kubebuilder:validation:Optional
	ManagerControl *UpgradeManagerControlStatus `json:"managerControl,omitempty"`

	// WorkerControl is the worker-owned effective acknowledgement of manager
	// admission and control. Native admission must keep its ownership disjoint
	// from ManagerAdmission and ManagerControl.
	// +kubebuilder:validation:Optional
	WorkerControl *UpgradeWorkerControlStatus `json:"workerControl,omitempty"`

	// ManagerDrain is the manager-owned, opt-in PDB-aware drain snapshot. Its
	// absence preserves existing scheduling and BlockIfRunning behavior.
	// +kubebuilder:validation:Optional
	ManagerDrain *UpgradeManagerDrainStatus `json:"managerDrain,omitempty"`

	// WorkerDrain is the worker-owned device inventory acknowledgement for the
	// exact ManagerDrain session.
	// +kubebuilder:validation:Optional
	WorkerDrain *UpgradeWorkerDrainStatus `json:"workerDrain,omitempty"`

	// ManagedMutationClaims binds every durable at-most-once mutation marker to
	// the reservation/control revision it claimed. The worker adds an entry in
	// the same resourceVersion update as the corresponding existing marker.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxItems=6
	// +listType=map
	// +listMapKey=stage
	ManagedMutationClaims []UpgradeManagedMutationClaimStatus `json:"managedMutationClaims,omitempty"`

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
	// +kubebuilder:validation:MaxItems=64
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
