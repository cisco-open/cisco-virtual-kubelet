// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

const UpgradeExecutionModelCatalystCenterReloadV1 UpgradeExecutionModel = "CatalystCenterReloadV1"

const UpgradeExecutionModelCatalystCenterV1 UpgradeExecutionModel = "CatalystCenterV1"
const UpgradeExecutionModelCatalystCenterPreparationV1 UpgradeExecutionModel = "CatalystCenterPreparationV1"

// CatalystCenterImageSource selects controller execution explicitly. It is a
// separate source union member so released workers reject it rather than
// silently performing a direct upgrade. All references are same-namespace.
type CatalystCenterImageSource struct {
	// StandardReloadProfile opts into the release-qualified normal-reload contract.
	// It selects no optional activation features and permits only the narrowly
	// recognized xFSU version-path warning. Omission retains strict readiness.
	// +optional
	// +kubebuilder:validation:Enum=CatalystCenter323
	StandardReloadProfile string `json:"standardReloadProfile,omitempty"`

	// Preparation pins an immutable administrator-owned ConfigMap policy.
	// Omission preserves the existing controller execution profile.
	// +optional
	Preparation *SWIMPreparationPolicyRef `json:"preparation,omitempty"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ControllerName string `json:"controllerName"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ControllerUID string `json:"controllerUID"`
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-]{1,128}$`
	// +kubebuilder:validation:MaxLength=253
	DeviceID string `json:"deviceID"`
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9_-]{1,128}$`
	// +kubebuilder:validation:MaxLength=253
	ImageID string `json:"imageID"`
	// ImageVersion is the exact controller inventory version.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ImageVersion string `json:"imageVersion"`
	// TransferFallbackAddress qualifies only the known positive HTTPS/SCP check.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	TransferFallbackAddress string `json:"transferFallbackAddress,omitempty"`
}

type SWIMPreparationPolicyRef struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	UID string `json:"uid"`
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	SHA256 string `json:"sha256"`
}

// UpgradeControllerHandoffStatus is written ONLY by the device worker through
// the existing leaf status admission boundary. It is never a controller grant
// to acquire a second lease. Each dispatch requires the live parent claim too.
type UpgradeControllerHandoffStatus struct {
	// Preparation is device-worker-owned evidence under the parent's lease.
	// +optional
	// +kubebuilder:validation:MaxItems=2
	Preparation []SWIMDevicePreparation `json:"preparation,omitempty"`
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +kubebuilder:validation:MaxLength=253
	UID string `json:"uid"`
	// Stage is Readiness, Staging, or PrimaryActivation.
	// +kubebuilder:validation:MaxLength=253
	Stage string `json:"stage"`
	// +kubebuilder:validation:MaxLength=253
	Token string `json:"token"`
	// +kubebuilder:validation:MaxLength=253
	WorkerPodUID    string      `json:"workerPodUID"`
	PolicyEpoch     int64       `json:"policyEpoch"`
	ControlRevision int64       `json:"controlRevision"`
	ExpiresAt       metav1.Time `json:"expiresAt"`
	// +optional
	// +kubebuilder:validation:MaxLength=253
	VerifiedVersion string `json:"verifiedVersion,omitempty"`
	// +optional
	VerifiedAt *metav1.Time `json:"verifiedAt,omitempty"`
}

// CatalystCenterSWIMHandoff is a device-worker-created delegation journal.
// It is not a user-created alternative to IOSXESoftwareRollout admission.
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Namespaced,shortName=swimhandoff
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Upgrade",type=string,JSONPath=`.spec.upgradeName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
type CatalystCenterSWIMHandoff struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              CatalystCenterSWIMHandoffSpec   `json:"spec"`
	Status            CatalystCenterSWIMHandoffStatus `json:"status,omitempty"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="handoff spec is immutable"
type CatalystCenterSWIMHandoffSpec struct {
	// +kubebuilder:validation:MaxLength=253
	UpgradeName string `json:"upgradeName"`
	// +kubebuilder:validation:MaxLength=253
	UpgradeUID string `json:"upgradeUID"`
	// +kubebuilder:validation:MaxLength=253
	DeviceName string `json:"deviceName"`
	// +kubebuilder:validation:MaxLength=253
	DeviceUID string `json:"deviceUID"`
	// +kubebuilder:validation:MaxLength=253
	Serial               string `json:"serial"`
	ControllerGeneration int64  `json:"controllerGeneration"`
	// +kubebuilder:validation:MaxLength=253
	ControllerUsername string `json:"controllerUsername"`
	// +kubebuilder:validation:MaxLength=253
	DeviceWorkerUsername string `json:"deviceWorkerUsername"`
	// +kubebuilder:validation:MaxLength=253
	DeviceWorkerPodUID string                    `json:"deviceWorkerPodUID"`
	Source             CatalystCenterImageSource `json:"source"`
	// +kubebuilder:validation:MaxLength=253
	TargetVersion string `json:"targetVersion"`
}

// Only the pinned controller worker writes the journal. The XE worker writes
// the grant and fresh verification on the parent, preserving single ownership.
type CatalystCenterSWIMHandoffStatus struct {
	// +optional
	// +kubebuilder:validation:MaxItems=2
	InventorySyncs []SWIMInventorySync `json:"inventorySyncs,omitempty"`
	// ReadinessHistory preserves completed checks replaced after an explicit
	// parent reauthorization. At most two replacements per stage are allowed.
	// +optional
	// +kubebuilder:validation:MaxItems=5
	ReadinessHistory []SWIMReadinessReceipt `json:"readinessHistory,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Phase string `json:"phase,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=253
	DistributionClaim string `json:"distributionClaim,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=253
	DistributionTask string `json:"distributionTask,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=253
	ActivationClaim string `json:"activationClaim,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=253
	ActivationTask string `json:"activationTask,omitempty"`
	// +optional
	ActivationNotBefore *metav1.Time `json:"activationNotBefore,omitempty"`
	// +optional
	DistributionNotBefore *metav1.Time `json:"distributionNotBefore,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=253
	ReadinessTask string `json:"readinessTask,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=253
	ReadinessClaim string `json:"readinessClaim,omitempty"`
	// +optional
	// +kubebuilder:validation:MaxLength=253
	ReadinessFor string `json:"readinessFor,omitempty"`
	// +optional
	ReadinessNotBefore *metav1.Time `json:"readinessNotBefore,omitempty"`
}

type SWIMInventorySync struct {
	// +kubebuilder:validation:MaxLength=253
	Stage string `json:"stage"`
	// +kubebuilder:validation:MaxLength=253
	PreparationID string `json:"preparationID"`
	// +kubebuilder:validation:MaxLength=253
	Claim       string      `json:"claim"`
	SubmittedAt metav1.Time `json:"submittedAt"`
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Task string `json:"task,omitempty"`
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

type SWIMDevicePreparation struct {
	// +kubebuilder:validation:MaxLength=253
	ID string `json:"id"`
	// +kubebuilder:validation:Enum=ReadyToDistribute;ReadyToActivate
	Stage string `json:"stage"`
	// +kubebuilder:validation:Enum=Planned;Removing;Complete;OutcomeUnknown
	Phase string `json:"phase"`
	// +kubebuilder:validation:MaxLength=64
	PolicySHA256 string `json:"policySHA256"`
	// +kubebuilder:validation:MaxLength=64
	PlanSHA256      string `json:"planSHA256"`
	FreeBytesBefore int64  `json:"freeBytesBefore"`
	// +optional
	FreeBytesAfter int64 `json:"freeBytesAfter,omitempty"`
	// +kubebuilder:validation:MaxItems=3
	Files      []SWIMPreparationFile `json:"files"`
	ObservedAt metav1.Time           `json:"observedAt"`
	// +optional
	CompletedAt *metav1.Time `json:"completedAt,omitempty"`
}

type SWIMPreparationFile struct {
	// +kubebuilder:validation:MaxLength=253
	Path string `json:"path"`
	// +kubebuilder:validation:MaxLength=64
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	// +kubebuilder:validation:MaxLength=253
	ReceiptUID string `json:"receiptUID"`
	// +optional
	ClaimedAt *metav1.Time `json:"claimedAt,omitempty"`
	// +optional
	RemovedAt *metav1.Time `json:"removedAt,omitempty"`
}

type SWIMReadinessReceipt struct {
	// +kubebuilder:validation:MaxLength=253
	Task string `json:"task"`
	// +kubebuilder:validation:MaxLength=253
	Claim string `json:"claim"`
	// +kubebuilder:validation:Enum=ReadyToDistribute;ReadyToActivate
	Stage       string      `json:"stage"`
	SubmittedAt metav1.Time `json:"submittedAt"`
}

// +kubebuilder:object:root=true
type CatalystCenterSWIMHandoffList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CatalystCenterSWIMHandoff `json:"items"`
}

func init() { SchemeBuilder.Register(&CatalystCenterSWIMHandoff{}, &CatalystCenterSWIMHandoffList{}) }
