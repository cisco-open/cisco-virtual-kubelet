// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadGroupMemberCancellation corroborates one exact member acknowledgement
// against the durable cancellation fence. Partial creation is permitted; this
// read proves neither lease cleanup nor complete group settlement.
func ReadGroupMemberCancellation(ctx context.Context, reader client.Reader, member *ops.ControllerConfigurationReservation, now time.Time) error {
	if reader == nil || member == nil || member.UID == "" || member.ResourceVersion == "" {
		return errors.New("group cancellation member is unavailable")
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: member.Namespace, Name: member.Spec.Controller.Name}, &nc); err != nil {
		return err
	}
	current, rows, err := readGroupSettlementObjects(ctx, reader, &nc, []*ops.ControllerConfigurationReservation{member})
	if err != nil {
		return err
	}
	if current.ResourceVersion != nc.ResourceVersion || rows[0].ResourceVersion != member.ResourceVersion || !equality.Semantic.DeepEqual(rows[0].Status, member.Status) {
		return errors.New("group cancellation evidence changed")
	}
	return ValidateGroupCancellationAcknowledgement(current, rows[0], now)
}

// ValidateGroupCancellationMember validates one existing member against the
// complete journal cancellation identity. Other reservations need not yet exist:
// cancellation can interrupt their creation. This proves neither group cleanup
// nor absence of uncreated members; those require separate complete-set checks.
func ValidateGroupCancellationMember(nc *cvk.NetworkController, r *ops.ControllerConfigurationReservation, now time.Time) error {
	if err := ValidateGroupCancellationFence(nc, now); err != nil {
		return err
	}
	reject := errors.New("group member cannot acknowledge this cancellation")
	if r == nil {
		return reject
	}
	targets, err := CanonicalGroupTargets(nc.Status.Configuration.Operation.PhysicalGroup.Targets)
	if err != nil {
		return err
	}
	copy := r.DeepCopy()
	copy.DeletionTimestamp = nil
	found := false
	for _, target := range targets {
		if target.PhysicalIdentity == r.Spec.PhysicalIdentity {
			if err := validateGroupReservationMember(nc, copy, target); err != nil {
				return err
			}
			found = true
			break
		}
	}
	if !found || r.Status.Completion != nil || r.Status.PreparationCancelledAt != nil {
		return reject
	}
	if claim := r.Status.Claim; claim != nil {
		if r.Status.Grant == nil || claim.OperationID != r.Spec.OperationID || claim.OperationDigest != r.Spec.OperationDigest || claim.ClaimedAt.IsZero() || claim.ClaimedAt.Before(&r.Status.Grant.GrantedAt) || claim.ClaimedAt.After(now) {
			return reject
		}
	}
	if g := r.Status.Grant; g != nil {
		holder, err := Holder(r.Namespace, r)
		if err != nil || g.Holder != holder || g.GrantedAt.IsZero() || g.GrantedAt.After(now) || (g.Phase != "Held" && g.Phase != "Revoked" && g.Phase != "Released") {
			return reject
		}
	}
	return nil
}

// AcknowledgeGroupCancellation freezes one member's grant/claim presence after
// the durable journal fence. It preserves existing claims and grants and grants
// no cleanup permission. The bound device-worker actor/admission must be checked
// by authorize. A concurrent grant/claim wins its CAS instead of being erased.
func AcknowledgeGroupCancellation(ctx context.Context, reader client.Reader, writer client.StatusClient, controller *cvk.NetworkController, expected *ops.ControllerConfigurationReservation, podUID string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) error {
	reject := errors.New("group cancellation acknowledgement lacks current authority")
	if reader == nil || writer == nil || controller == nil || controller.UID == "" || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || podUID == "" || authorize == nil || now.IsZero() {
		return reject
	}
	read := func() (*cvk.NetworkController, *ops.ControllerConfigurationReservation, error) {
		nc, members, err := readGroupSettlementObjects(ctx, reader, controller, []*ops.ControllerConfigurationReservation{expected})
		if err != nil {
			return nil, nil, err
		}
		r := members[0]
		if r.ResourceVersion != expected.ResourceVersion || r.Spec.DeviceWorkerPod.UID != podUID {
			return nil, nil, reject
		}
		if err := ValidateGroupCancellationMember(nc, r, now); err != nil {
			return nil, nil, err
		}
		return nc, r, nil
	}
	_, r, err := read()
	if err != nil {
		return err
	}
	if err := authorize(ctx, r.DeepCopy()); err != nil {
		return err
	}
	nc, r, err := read()
	if err != nil {
		return err
	}
	op := nc.Status.Configuration.Operation
	if r.Status.GroupCancellation != nil {
		return ValidateGroupCancellationAcknowledgement(nc, r, now)
	}
	if r.Status.ManagerSettledAt != nil {
		return reject
	}
	r.Status.GroupCancellation = &ops.ConfigurationReservationGroupCancellation{OperationID: op.ID, OperationDigest: op.PhysicalGroup.Digest, RequestedAt: *op.CancellationRequestedAt.DeepCopy(), AcknowledgedAt: metav1.NewTime(now.UTC().Truncate(time.Second))}
	if err := ValidateGroupCancellationAcknowledgement(nc, r, now); err != nil {
		return err
	}
	intended := r.DeepCopy()
	if err := writer.Status().Update(ctx, r); err != nil {
		return err
	}
	var persisted ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, client.ObjectKeyFromObject(intended), &persisted); err != nil {
		return err
	}
	if persisted.UID != intended.UID || !reflect.DeepEqual(persisted.Spec, intended.Spec) || !equality.Semantic.DeepEqual(persisted.Status, intended.Status) {
		return reject
	}
	return ValidateGroupCancellationAcknowledgement(nc, &persisted, now)
}

// ValidateGroupCancellationAcknowledgement binds retained member evidence to
// the complete durable cancellation fence. It does not prove lease cleanup or
// manager settlement and is safe to use only as one prerequisite to those checks.
func ValidateGroupCancellationAcknowledgement(nc *cvk.NetworkController, r *ops.ControllerConfigurationReservation, now time.Time) error {
	if err := ValidateGroupCancellationMember(nc, r, now); err != nil {
		return err
	}
	c := r.Status.GroupCancellation
	op := nc.Status.Configuration.Operation
	if c == nil || c.OperationID != op.ID || c.OperationDigest != op.PhysicalGroup.Digest || !c.RequestedAt.Equal(op.CancellationRequestedAt) || c.AcknowledgedAt.IsZero() || c.AcknowledgedAt.Before(&c.RequestedAt) || c.AcknowledgedAt.After(now) {
		return errors.New("group cancellation acknowledgement does not match journal fence")
	}
	return nil
}
