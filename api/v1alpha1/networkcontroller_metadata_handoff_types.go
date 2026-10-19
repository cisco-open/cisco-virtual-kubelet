// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package v1alpha1

import (
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const NetworkControllerMetadataHandoffProtocol = "controller-metadata-handoff-v1"

// NetworkControllerConfigurationMetadataHandoff retains a preparation and
// certified non-dispatch independently of its journal slot. This is not an
// application receipt or a dispatch grant. Actor admission and retained fence
// lifecycle must be installed before a manager may enable this protocol.
// +kubebuilder:validation:XValidation:rule="self.operation.phase == 'Prepared' && self.operation.action == 'Delete' && !has(self.operation.submittedAt) && (!has(self.operation.targetSerial) || size(self.operation.targetSerial) == 0) && !has(self.operation.physicalGroup) && (!has(self.operation.taskID) || size(self.operation.taskID) == 0) && (!has(self.operation.createdResourceID) || size(self.operation.createdResourceID) == 0)",message="metadata handoff requires an unsubmitted metadata Delete preparation"
// +kubebuilder:validation:XValidation:rule="has(self.refusedAt) == has(self.refusedSubmissionAt) && (!has(self.refusedAt) || self.refusedAt >= self.refusedSubmissionAt)",message="certified refusal requires its original submission time"
// +kubebuilder:validation:XValidation:rule="!has(self.cancelledAt) || (has(self.readyAt) && self.cancelledAt >= self.readyAt && !has(self.refusedAt))",message="prepared cancellation requires original ready custody and no submission refusal"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.cancelledAt) || (has(self.cancelledAt) && self.cancelledAt == oldSelf.cancelledAt)",message="prepared cancellation is immutable"
// +kubebuilder:validation:XValidation:rule="self.protocol == oldSelf.protocol && self.bindingDigest == oldSelf.bindingDigest && self.leaseNamespace == oldSelf.leaseNamespace && self.workerPod == oldSelf.workerPod && self.workerUsername == oldSelf.workerUsername && self.physicalIdentities == oldSelf.physicalIdentities && self.operation == oldSelf.operation",message="metadata handoff binding is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.refusedAt) || (has(self.refusedAt) && self.refusedAt == oldSelf.refusedAt && self.refusedSubmissionAt == oldSelf.refusedSubmissionAt)",message="certified refusal evidence is immutable"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.releasedAt) || (has(self.releasedAt) && self.releasedAt == oldSelf.releasedAt)",message="manager release acknowledgement is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.fences) || (self.fences.size() <= self.physicalIdentities.size() && self.fences.all(f, f.physicalIdentity in self.physicalIdentities))",message="metadata fences must belong to the sealed physical cohort"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.fences) || (has(self.fences) && oldSelf.fences.all(original, self.fences.exists(f, f == original)))",message="original metadata fence incarnations are immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.readyAt) || (has(self.fences) && self.fences.size() == self.physicalIdentities.size())",message="metadata readiness requires the complete original fence cohort"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.readyAt) || (has(self.readyAt) && self.readyAt == oldSelf.readyAt)",message="manager metadata readiness is immutable"
// +kubebuilder:validation:XValidation:rule="!has(self.settlementDigest) || has(self.readyAt)",message="metadata settlement requires original ready custody"
// +kubebuilder:validation:XValidation:rule="!has(oldSelf.settlementDigest) || (has(self.settlementDigest) && self.settlementDigest == oldSelf.settlementDigest)",message="original metadata settlement intent is immutable"
type NetworkControllerConfigurationMetadataHandoff struct {
	// +kubebuilder:validation:Enum=controller-metadata-handoff-v1
	Protocol string `json:"protocol"`
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	// +kubebuilder:validation:MaxLength=64
	BindingDigest string `json:"bindingDigest"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	LeaseNamespace string                                `json:"leaseNamespace"`
	WorkerPod      ops.ConfigurationReservationReference `json:"workerPod"`
	// WorkerUsername is sealed from the verified worker ServiceAccount identity.
	// +kubebuilder:validation:Pattern=`^system:serviceaccount:[a-z0-9-]+:[a-z0-9.-]+$`
	// +kubebuilder:validation:MaxLength=320
	WorkerUsername string `json:"workerUsername"`
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MinLength=1
	// +kubebuilder:validation:items:MaxLength=128
	// +listType=set
	PhysicalIdentities []string `json:"physicalIdentities"`
	// Fences records the original canonical Lease incarnations, published once by
	// the manager as acquisition progresses. Existing entries cannot change or
	// disappear; partial cohorts grant no dispatch.
	// +optional
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=256
	// +listType=map
	// +listMapKey=physicalIdentity
	Fences    []NetworkControllerMetadataFenceReference `json:"fences,omitempty"`
	Operation NetworkControllerConfigurationOperation   `json:"operation"`
	// ReadyAt acknowledges the manager completed original-cohort acquisition
	// and source/actor/consumer checks with all fences held. It is not a worker
	// claim; the current runtime and original journal CAS are still required.
	// +optional
	ReadyAt *metav1.Time `json:"readyAt,omitempty"`
	// RefusedSubmissionAt preserves the original Submitting boundary; it never
	// denotes a successful API invocation or a native task acknowledgement.
	// +optional
	RefusedSubmissionAt *metav1.Time `json:"refusedSubmissionAt,omitempty"`
	// RefusedAt is written only while consuming the original one-use pre-HTTP
	// guard proof, atomically with clearing that exact journal operation.
	// +optional
	RefusedAt *metav1.Time `json:"refusedAt,omitempty"`
	// CancelledAt records the manager's atomic cancellation of the exact original
	// Prepared journal before any worker claim. It is not a native outcome or a
	// pre-HTTP refusal proof. Ready original custody is settled separately.
	// +optional
	CancelledAt *metav1.Time `json:"cancelledAt,omitempty"`
	// SettlementDigest is the manager's durable terminal-outcome commitment,
	// published before any original fence is marked or deleted.
	// +optional
	// +kubebuilder:validation:Pattern=`^[a-f0-9]{64}$`
	// +kubebuilder:validation:MaxLength=64
	SettlementDigest string `json:"settlementDigest,omitempty"`
	// ReleasedAt is reserved for manager acknowledgement after exact fence release.
	// No writer or runtime activation is enabled by declaring this optional field.
	// +optional
	ReleasedAt *metav1.Time `json:"releasedAt,omitempty"`
}

// NetworkControllerMetadataFenceReference retains one canonical bootstrap Lease
// incarnation in MetadataHandoff.LeaseNamespace. UID prevents replacement adoption.
type NetworkControllerMetadataFenceReference struct {
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	PhysicalIdentity string `json:"physicalIdentity"`
	// +kubebuilder:validation:Pattern=`^cvk-bootstrap-[a-f0-9]{32}$`
	// +kubebuilder:validation:MaxLength=46
	Name string `json:"name"`
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=128
	UID string `json:"uid"`
}
