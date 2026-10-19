// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// FinishCancelledControllerGroup resolves all canonical journal members before
// invoking cancellation settlement and its atomic retained receipt writer.
func FinishCancelledControllerGroup(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) error {
	if authorize == nil {
		return errors.New("group cancellation cleanup authority unavailable")
	}
	members, err := readJournalGroupReservations(ctx, reader, expected)
	if err != nil {
		return err
	}
	return FinishCancelledGroup(ctx, reader, writer, expected, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		for _, member := range current {
			if err := authorize(ctx, member); err != nil {
				return err
			}
		}
		return nil
	}, now)
}

// FinishCancelledGroup atomically retains cancellation evidence and clears its
// exact journal slot after ALL member reservations have acknowledged cancellation
// and settled. It cannot skip absent reservations: the manager must first retain
// missing members through EnsureCancelledGroupReservations. Authorize must check the
// installed group admission, retention and actor contract. No retries or remote
// calls are made. Group runtime remains disabled.
func FinishCancelledGroup(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	reject := errors.New("group journal cannot clear before exact complete settlement")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || expected.Status.Configuration == nil || expected.Status.Configuration.Operation == nil || authorize == nil || now.IsZero() {
		return reject
	}
	op := expected.Status.Configuration.Operation
	if op.Phase != "Cancelling" || ValidateGroupJournalIdentity(op) != nil || len(members) == 0 || members[0] == nil || members[0].Spec.OperationID != op.ID {
		return reject
	}
	read := func() (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, error) {
		nc, current, err := readGroupSettlementObjects(ctx, reader, expected, members)
		if err != nil {
			return nil, nil, err
		}
		if nc.ResourceVersion != expected.ResourceVersion || nc.Status.Configuration == nil || !equality.Semantic.DeepEqual(nc.Status.Configuration.Operation, op) {
			return nil, nil, reject
		}
		if err := validateCompleteGroupCancellationSettlement(nc, current, now); err != nil {
			return nil, nil, err
		}
		return nc, current, nil
	}
	_, current, err := read()
	if err != nil {
		return err
	}
	if err := authorize(ctx, copyGroupReservations(current)); err != nil {
		return err
	}
	nc, current, err := read()
	if err != nil {
		return err
	}
	if len(nc.Status.Configuration.GroupCancellations) >= 32 {
		return errors.New("group cancellation retention is full")
	}
	for _, receipt := range nc.Status.Configuration.GroupCancellations {
		if receipt.OperationID == op.ID {
			return errors.New("group cancellation receipt already exists")
		}
	}
	ordered, err := ValidateGroupReservationSet(nc, current)
	if err != nil {
		return err
	}
	receipt := cvk.NetworkControllerConfigurationGroupCancellation{OperationID: op.ID, Operation: *op.DeepCopy(), SettledAt: metav1.NewTime(now.UTC().Truncate(time.Second))}
	for _, member := range ordered {
		receipt.Members = append(receipt.Members, ops.ConfigurationReservationReference{Name: member.Name, UID: string(member.UID)})
	}
	nc.Status.Configuration.GroupCancellations = append(nc.Status.Configuration.GroupCancellations, receipt)
	nc.Status.Configuration.Operation = nil
	if err := ValidateRetainedGroupCancellation(nc, op.ID, current, now); err != nil {
		return err
	}
	retained := nc.Status.Configuration.DeepCopy()
	if err := writer.Status().Update(ctx, nc); err != nil {
		return err
	}
	var persisted cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &persisted); err != nil {
		return err
	}
	if persisted.UID != expected.UID || !equality.Semantic.DeepEqual(persisted.Status.Configuration, retained) {
		return reject
	}
	return nil
}

func validateCompleteGroupCancellationSettlement(nc *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, now time.Time) error {
	if err := ValidateGroupCancellationFence(nc, now); err != nil {
		return err
	}
	ordered, err := ValidateGroupReservationSet(nc, members)
	if err != nil {
		return err
	}
	for _, member := range ordered {
		if err := ValidateCancelledGroupMemberSettlement(nc, member, now); err != nil {
			return err
		}
	}
	return nil
}

// ValidateRetainedGroupCancellation audits the complete cancellation after the
// operation slot has advanced. It never supplies dispatch or deletion authority.
func ValidateRetainedGroupCancellation(nc *cvk.NetworkController, operationID string, members []*ops.ControllerConfigurationReservation, now time.Time) error {
	reject := errors.New("complete retained group cancellation evidence is unavailable")
	if nc == nil || nc.Status.Configuration == nil || now.IsZero() {
		return reject
	}
	var receipt *cvk.NetworkControllerConfigurationGroupCancellation
	for i := range nc.Status.Configuration.GroupCancellations {
		candidate := &nc.Status.Configuration.GroupCancellations[i]
		if candidate.OperationID == operationID {
			if receipt != nil {
				return reject
			}
			receipt = candidate
		}
	}
	if receipt == nil || receipt.Operation.ID != operationID || receipt.SettledAt.IsZero() || receipt.SettledAt.After(now) || len(receipt.Members) != len(members) {
		return reject
	}
	if active := nc.Status.Configuration.Operation; active != nil && active.ID == operationID && !equality.Semantic.DeepEqual(active, &receipt.Operation) {
		return reject
	}
	audit := nc.DeepCopy()
	audit.Status.Configuration.Operation = receipt.Operation.DeepCopy()
	// Historical evidence remains inspectable while finalizers hold deletion.
	// This does not relax live closure or authorize removing those finalizers.
	auditMembers := make([]*ops.ControllerConfigurationReservation, 0, len(members))
	for _, member := range members {
		if member == nil {
			return reject
		}
		copy := member.DeepCopy()
		copy.DeletionTimestamp = nil
		auditMembers = append(auditMembers, copy)
	}
	if err := validateCompleteGroupCancellationSettlement(audit, auditMembers, now); err != nil {
		return err
	}
	refs := make(map[string]string, len(receipt.Members))
	for _, ref := range receipt.Members {
		if ref.Name == "" || ref.UID == "" || refs[ref.Name] != "" {
			return reject
		}
		refs[ref.Name] = ref.UID
	}
	for _, member := range members {
		if refs[member.Name] != string(member.UID) || receipt.SettledAt.Before(member.Status.ManagerSettledAt) {
			return reject
		}
	}
	return nil
}
