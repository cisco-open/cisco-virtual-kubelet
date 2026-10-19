// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package v1alpha1

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// ControllerConfigurationReservation separates device-worker lease authority
// from controller-worker API execution. This contract alone grants no authority:
// manager creation, actor-specific admission, lease renewal and recovery must be
// installed before workers can use it. References are in metadata.namespace,
// except the cluster-scoped Node and explicitly namespaced canonical Lease.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced
// +kubebuilder:validation:XValidation:rule="!has(self.status) || !has(self.status.groupCancellation) || (self.spec.protocol == 'controller-configuration-group-v1' && self.status.groupCancellation.operationID == self.spec.operationID && self.status.groupCancellation.operationDigest == self.spec.operationDigest)",message="group cancellation requires the exact group reservation binding"
type ControllerConfigurationReservation struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ControllerConfigurationReservationSpec `json:"spec"`
	// +optional
	Status ControllerConfigurationReservationStatus `json:"status,omitempty"`
}

type ConfigurationReservationReference struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="configuration reservation binding is immutable"
type ControllerConfigurationReservationSpec struct {
	// +kubebuilder:validation:Enum=controller-configuration-v1;controller-configuration-group-v1
	Protocol            string                            `json:"protocol"`
	Controller          ConfigurationReservationReference `json:"controller"`
	Config              ConfigurationReservationReference `json:"config"`
	Device              ConfigurationReservationReference `json:"device"`
	Node                ConfigurationReservationReference `json:"node"`
	ControllerWorkerPod ConfigurationReservationReference `json:"controllerWorkerPod"`
	DeviceWorkerPod     ConfigurationReservationReference `json:"deviceWorkerPod"`
	// +kubebuilder:validation:Pattern=`^system:serviceaccount:[a-z0-9-]+:[a-z0-9.-]+$`
	ControllerUsername string `json:"controllerUsername"`
	// +kubebuilder:validation:Pattern=`^system:serviceaccount:[a-z0-9-]+:[a-z0-9.-]+$`
	DeviceWorkerUsername string `json:"deviceWorkerUsername"`
	// +kubebuilder:validation:Minimum=1
	ControllerGeneration int64 `json:"controllerGeneration"`
	// +kubebuilder:validation:Minimum=1
	ConfigGeneration int64 `json:"configGeneration"`
	// +kubebuilder:validation:Minimum=1
	DeviceGeneration int64 `json:"deviceGeneration"`
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{32}$`
	OperationID string `json:"operationID"`
	// OperationDigest binds the exact non-secret journal operation and source
	// revision. It is not a digest of intent payloads or credential material.
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	OperationDigest string `json:"operationDigest"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	PhysicalIdentity string `json:"physicalIdentity"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	InventoryID string `json:"inventoryID"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	ManagementAddress string `json:"managementAddress"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=256
	DeviceWorkerRevision string `json:"deviceWorkerRevision"`
}

// +kubebuilder:validation:XValidation:rule="!has(oldSelf.managerSettledAt) || (has(self.managerSettledAt) && self.managerSettledAt == oldSelf.managerSettledAt)",message="manager settlement evidence is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.managerSettledAt) || ((has(self.grant) && self.grant.phase == 'Released' && (!has(self.claim) || has(self.completion) || has(self.groupCancellation))) || (has(self.groupCancellation) && !has(self.grant) && !has(self.claim)) || (has(self.preparationCancelledAt) && !has(self.grant) && !has(self.claim) && !has(self.completion)))",message="manager settlement requires released or cancelled operation evidence"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.preparationCancelledAt) || (has(self.preparationCancelledAt) && self.preparationCancelledAt == oldSelf.preparationCancelledAt)",message="preparation cancellation is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.preparationCancelledAt) || (!has(self.grant) && !has(self.claim) && !has(self.completion))",message="cancelled preparation cannot have a grant, claim, or completion"
// +kubebuilder:validation:XValidation:rule="!has(self.groupCancellation) || (!has(self.completion) && !has(self.preparationCancelledAt) && (!has(self.claim) || has(self.grant)))",message="group cancellation cannot invent completion or lose claimed grant evidence"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.groupCancellation) || (has(self.groupCancellation) && self.groupCancellation == oldSelf.groupCancellation)",message="group cancellation acknowledgement is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.groupCancellation) || (has(self.claim) == has(oldSelf.claim) && (!has(self.claim) || self.claim == oldSelf.claim) && has(self.grant) == has(oldSelf.grant))",message="acknowledged group cancellation forbids new claims or grants"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.groupCancellation) || !has(oldSelf.grant) || !has(self.grant) || (self.grant.leaseNamespace == oldSelf.grant.leaseNamespace && self.grant.lease == oldSelf.grant.lease && self.grant.holder == oldSelf.grant.holder && self.grant.leaseTransitions == oldSelf.grant.leaseTransitions && self.grant.grantedAt == oldSelf.grant.grantedAt && (self.grant.phase == oldSelf.grant.phase || self.grant.phase == 'Released' || (oldSelf.grant.phase == 'Held' && self.grant.phase == 'Revoked')))",message="cancelled grant identity is frozen and release cannot be reversed"
type ControllerConfigurationReservationStatus struct {
	// GroupCancellation acknowledges the durable journal cancellation fence.
	// Existing claims remain evidence; this does not prove lease release.
	// +optional
	GroupCancellation *ConfigurationReservationGroupCancellation `json:"groupCancellation,omitempty"`

	// PreparationCancelledAt closes an ungranted reservation permanently.
	// Written only by its bound device worker; it is not evidence of Lease or
	// maintenance-session release. Manager settlement is still required.
	// +optional
	PreparationCancelledAt *metav1.Time `json:"preparationCancelledAt,omitempty"`

	// ManagerSettledAt retains manager settlement after the device advances to a
	// later maintenance session. It is set once, after the exact session settles.
	// +optional
	ManagerSettledAt *metav1.Time `json:"managerSettledAt,omitempty"`

	// Grant is written only by the bound device worker. A live matching Lease
	// and managed identity are required in addition to this status evidence.
	// +optional
	Grant *ConfigurationReservationGrant `json:"grant,omitempty"`
	// Claim is written only by the bound controller worker in a CAS against an
	// active grant, before remote submission. It never authorizes a retry.
	// +optional
	Claim *ConfigurationReservationClaim `json:"claim,omitempty"`
	// Completion is controller-written evidence of a verified, persisted action receipt.
	// The device worker must independently read that receipt before releasing.
	// +optional
	Completion *ConfigurationReservationCompletion `json:"completion,omitempty"`
}

type ConfigurationReservationGrant struct {
	// +kubebuilder:validation:Enum=Held;Revoked;Released
	Phase string `json:"phase"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	LeaseNamespace string                            `json:"leaseNamespace"`
	Lease          ConfigurationReservationReference `json:"lease"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	Holder string `json:"holder"`
	// +kubebuilder:validation:Minimum=0
	LeaseTransitions int32       `json:"leaseTransitions"`
	GrantedAt        metav1.Time `json:"grantedAt"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="configuration submission claim is immutable"
type ConfigurationReservationClaim struct {
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{32}$`
	OperationID string `json:"operationID"`
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	OperationDigest string      `json:"operationDigest"`
	ClaimedAt       metav1.Time `json:"claimedAt"`
}

// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="configuration completion is immutable"
type ConfigurationReservationCompletion struct {
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{32}$`
	OperationID string `json:"operationID"`
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	OperationDigest string `json:"operationDigest"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	TaskID      string      `json:"taskID"`
	SubmittedAt metav1.Time `json:"submittedAt"`
	VerifiedAt  metav1.Time `json:"verifiedAt"`
}

// +kubebuilder:object:root=true
type ControllerConfigurationReservationList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ControllerConfigurationReservation `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ControllerConfigurationReservation{}, &ControllerConfigurationReservationList{})
}

// +kubebuilder:validation:XValidation:rule="self.acknowledgedAt >= self.requestedAt",message="cancellation acknowledgement cannot precede its journal fence"
type ConfigurationReservationGroupCancellation struct {
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{32}$`
	OperationID string `json:"operationID"`
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	OperationDigest string      `json:"operationDigest"`
	RequestedAt     metav1.Time `json:"requestedAt"`
	AcknowledgedAt  metav1.Time `json:"acknowledgedAt"`
}
