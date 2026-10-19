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
	coordination "k8s.io/api/coordination/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadClaimRetention validates an existing single-device claim and live lease
// against its retained journal. Like group retention, it grants no submission,
// refresh or release authority and requires neither current desired intent nor
// a new-submission freshness window. Those are separate execution gates.
func ReadClaimRetention(ctx context.Context, reader client.Reader, expected *ops.ControllerConfigurationReservation, lease *coordination.Lease, namespace string, now time.Time) error {
	reject := errors.New("claim retention lacks exact journal/reservation/lease evidence")
	if reader == nil || expected == nil || expected.UID == "" || expected.Spec.Protocol != Protocol || lease == nil || now.IsZero() {
		return reject
	}
	var reservation ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &reservation); err != nil {
		return err
	}
	if reservation.UID != expected.UID || reservation.ResourceVersion != expected.ResourceVersion || !reflect.DeepEqual(reservation.Spec, expected.Spec) || !reflect.DeepEqual(reservation.Status, expected.Status) {
		return reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: reservation.Namespace, Name: reservation.Spec.Controller.Name}, &nc); err != nil {
		return err
	}
	if string(nc.UID) != reservation.Spec.Controller.UID || nc.Status.Configuration == nil || ValidateOperationBinding(&reservation, nc.Status.Configuration.Operation) != nil {
		return reject
	}
	op := nc.Status.Configuration.Operation
	claim := reservation.Status.Claim
	if claim == nil || claim.OperationID != op.ID || claim.OperationDigest != reservation.Spec.OperationDigest || claim.ClaimedAt.IsZero() || claim.ClaimedAt.After(now) || (op.SubmittedAt != nil && op.SubmittedAt.Before(&claim.ClaimedAt)) || reservation.Status.ManagerSettledAt != nil || reservation.Status.Completion != nil {
		return reject
	}
	if err := validateLeaseGrantEvidence(&reservation, lease, namespace, now); err != nil {
		return err
	}
	for _, old := range []client.Object{&nc, &reservation, lease} {
		fresh := old.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(old), fresh); err != nil {
			return err
		}
		if fresh.GetUID() != old.GetUID() || fresh.GetResourceVersion() != old.GetResourceVersion() || !reflect.DeepEqual(fresh, old) {
			return reject
		}
	}
	return nil
}
