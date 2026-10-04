// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0

package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// IOSXESoftwareRolloutPreparationInvalidation names immutable receipts that
// may be retired only after cancellation, settlement and native quiescence.
// Native admission binds RequestedBy to the caller with the recover permission.
type IOSXESoftwareRolloutPreparationInvalidation struct {
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	PlanHash string `json:"planHash"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	RequestedBy string      `json:"requestedBy"`
	RequestedAt metav1.Time `json:"requestedAt"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Reason string `json:"reason"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=100
	// +listType=map
	// +listMapKey=deviceUID
	Receipts []IOSXESoftwareRolloutActivationReceipt `json:"receipts"`
}

// UpgradePreparedInvalidationRequest is manager-owned, append-only authority
// for one exact settled receipt. It permits observation, never a device RPC
// that modifies install state. It cannot be used for an activation outcome.
type UpgradePreparedInvalidationRequest struct {
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	ReceiptHash string `json:"receiptHash"`
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	PlanHash string `json:"planHash"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	CampaignUID string `json:"campaignUID"`
	// +kubebuilder:validation:Minimum=1
	ControlRevision int64 `json:"controlRevision"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	RequestedBy string      `json:"requestedBy"`
	RequestedAt metav1.Time `json:"requestedAt"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	Reason string `json:"reason"`
}

// UpgradePreparedInvalidationStatus retains native read-only proof alongside
// the original immutable receipt. A new campaign needs a new approval; neither
// this record nor the old receipt authorizes reuse or activation.
type UpgradePreparedInvalidationStatus struct {
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	RequestHash string `json:"requestHash"`
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	NativeEvidenceHash string      `json:"nativeEvidenceHash"`
	ObservedAt         metav1.Time `json:"observedAt"`
	// +kubebuilder:validation:Pattern=`^sha256:[a-f0-9]{64}$`
	WorkerRevision string `json:"workerRevision"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	WorkerPodUID string `json:"workerPodUID"`
	// RunningVersion must still be the receipt's original running version.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	RunningVersion string `json:"runningVersion"`
	// TargetState describes the inactive image; invalidation does not remove it.
	// +kubebuilder:validation:Enum=Absent;Installed
	TargetState string `json:"targetState"`
}
