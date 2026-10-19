// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"errors"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/types"
)

// GroupCompletionsForReceipt derives per-member completion from one retained,
// independently verified full-group action receipt. It requires every member;
// a subset cannot prove completion. The result is keyed by reservation UID, not
// a reusable name. This is evidence construction, never lease-release authority.
// The caller must read controller and reservations uncached and validate actors.
func GroupCompletionsForReceipt(nc *cvk.NetworkController, reservations []*ops.ControllerConfigurationReservation, now time.Time) (map[types.UID]*ops.ConfigurationReservationCompletion, error) {
	reject := errors.New("group completion requires a complete bound verified receipt")
	if nc == nil || nc.Status.Configuration == nil || len(reservations) == 0 || reservations[0] == nil || now.IsZero() {
		return nil, reject
	}
	receipt, err := verifiedGroupReceipt(nc, reservations[0].Spec.OperationID, now)
	if err != nil {
		return nil, err
	}
	op := &receipt.Operation
	// Retained receipt evidence survives a later operation/spec or deletion
	// request on the SAME controller/reservation UIDs. Only this audit ignores
	// deletion timestamps; live authority validation continues rejecting them.
	audit := nc.DeepCopy()
	audit.Status.Configuration.Operation = op.DeepCopy()
	copies := copyGroupReservationsForAudit(reservations)
	if copies == nil {
		return nil, reject
	}
	bound, err := ValidateGroupReservationSet(audit, copies)
	if err != nil {
		return nil, err
	}
	result := make(map[types.UID]*ops.ConfigurationReservationCompletion, len(bound))
	for _, r := range bound {
		completion, err := completionForGroupMember(receipt, r)
		if err != nil {
			return nil, err
		}
		result[r.UID] = completion
	}
	return result, nil
}

func copyGroupReservationsForAudit(reservations []*ops.ControllerConfigurationReservation) []*ops.ControllerConfigurationReservation {
	copies := make([]*ops.ControllerConfigurationReservation, len(reservations))
	for i, r := range reservations {
		if r == nil {
			return nil
		}
		copies[i] = r.DeepCopy()
		copies[i].DeletionTimestamp = nil
	}
	return copies
}

// ValidateGroupCompletionReceipts requires every persisted member completion to
// equal the shared verified receipt. Time passing or expired leases cannot
// substitute for receipt evidence, and one completed member cannot settle peers.
func ValidateGroupCompletionReceipts(nc *cvk.NetworkController, reservations []*ops.ControllerConfigurationReservation, now time.Time) error {
	expected, err := GroupCompletionsForReceipt(nc, reservations, now)
	if err != nil {
		return err
	}
	for _, r := range reservations {
		if r.Status.Completion == nil || !equality.Semantic.DeepEqual(expected[r.UID], r.Status.Completion) {
			return errors.New("group member completion does not match verified receipt")
		}
	}
	return nil
}

// A shared historical receipt validator for full-group completion and settled
// member retirement. It never grants dispatch or lease release authority.
func verifiedGroupReceipt(nc *cvk.NetworkController, operationID string, now time.Time) (*cvk.NetworkControllerConfigurationActionApplication, error) {
	reject := errors.New("group receipt is missing, ambiguous or invalid")
	if nc == nil || nc.Status.Configuration == nil || operationID == "" || now.IsZero() {
		return nil, reject
	}
	var receipt *cvk.NetworkControllerConfigurationActionApplication
	for i := range nc.Status.Configuration.ActionApplications {
		candidate := &nc.Status.Configuration.ActionApplications[i]
		if candidate.Operation.ID != operationID {
			continue
		}
		if receipt != nil {
			return nil, reject
		}
		receipt = candidate
	}
	if receipt == nil {
		return nil, reject
	}
	op := &receipt.Operation
	if receipt.Section != op.Section || receipt.Resource == "" || op.Phase != "Verified" || op.TaskID == "" || len(op.TaskID) > 128 || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || receipt.VerifiedAt == nil || receipt.VerifiedAt.IsZero() || receipt.VerifiedAt.Before(op.SubmittedAt) || receipt.VerifiedAt.After(now) {
		return nil, reject
	}
	if err := ValidateGroupJournalIdentity(op); err != nil {
		return nil, err
	}
	if active := nc.Status.Configuration.Operation; active != nil && active.ID == operationID && !equality.Semantic.DeepEqual(active, op) {
		return nil, reject
	}
	return receipt.DeepCopy(), nil
}

func completionForGroupMember(receipt *cvk.NetworkControllerConfigurationActionApplication, r *ops.ControllerConfigurationReservation) (*ops.ConfigurationReservationCompletion, error) {
	op := &receipt.Operation
	claim := r.Status.Claim
	grant := r.Status.Grant
	holder, holderErr := Holder(r.Namespace, r)
	if holderErr != nil || claim == nil || grant == nil || grant.Holder != holder || (grant.Phase != "Held" && grant.Phase != "Released") || r.Status.PreparationCancelledAt != nil || r.Status.GroupCancellation != nil || claim.OperationID != op.ID || claim.OperationDigest != op.PhysicalGroup.Digest || grant.GrantedAt.IsZero() || claim.ClaimedAt.IsZero() || claim.ClaimedAt.Before(&grant.GrantedAt) || op.SubmittedAt.Before(&claim.ClaimedAt) {
		return nil, errors.New("group member claim does not bind verified receipt")
	}
	return &ops.ConfigurationReservationCompletion{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, TaskID: op.TaskID, SubmittedAt: *op.SubmittedAt.DeepCopy(), VerifiedAt: *receipt.VerifiedAt.DeepCopy()}, nil
}
