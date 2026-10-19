// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"strings"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// BeginGroupVerification records that an acknowledged native task is ready for
// product convergence checks. Observe must establish current writer authority,
// owned intent and native task evidence. This grants no dispatch/release rights
// and writes no completion receipt. Expired leases do not prevent observation.
func BeginGroupVerification(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, observe func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	reject := errors.New("group verification requires an unchanged acknowledged operation and complete claims")
	if reader == nil || writer == nil || expected == nil || expected.ResourceVersion == "" || expected.Status.Configuration == nil || expected.Status.Configuration.Operation == nil || observe == nil || now.IsZero() {
		return reject
	}
	expected = expected.DeepCopy()
	members = copyGroupReservations(members)
	op := expected.Status.Configuration.Operation
	if (op.Phase != "Accepted" && op.Phase != "Unknown") || ValidateGroupJournalIdentity(op) != nil || !validGroupIdentityToken(op.TaskID) || strings.ContainsAny(op.TaskID, "/\\?#") || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) || op.CancellationRequestedAt != nil {
		return reject
	}
	read := func() (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, error) {
		nc, current, err := readGroupSettlementObjects(ctx, reader, expected, members)
		if err != nil {
			return nil, nil, err
		}
		if nc.Spec.Paused || nc.DeletionTimestamp != nil || nc.Generation != op.ControllerGeneration || nc.ResourceVersion != expected.ResourceVersion || nc.Status.Configuration == nil || !equality.Semantic.DeepEqual(nc.Status.Configuration.Operation, op) {
			return nil, nil, reject
		}
		bound, err := validateAcknowledgedGroupClaims(nc, current)
		if err != nil {
			return nil, nil, err
		}
		return nc, bound, nil
	}
	_, prior, err := read()
	if err != nil {
		return err
	}
	if err := observe(ctx, copyGroupReservations(prior)); err != nil {
		return err
	}
	next, fresh, err := read()
	if err != nil {
		return err
	}
	for i := range prior {
		if !equality.Semantic.DeepEqual(prior[i], fresh[i]) {
			return reject
		}
	}
	next.Status.Configuration.Operation.Phase = "Verifying"
	retained := next.Status.Configuration.DeepCopy()
	if err := writer.Status().Update(ctx, next); err != nil {
		return err
	}
	var stored cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(next), &stored); err != nil {
		return err
	}
	if stored.UID != expected.UID || !equality.Semantic.DeepEqual(stored.Status.Configuration, retained) {
		return reject
	}
	return nil
}

func validateAcknowledgedGroupClaims(nc *cvk.NetworkController, current []*ops.ControllerConfigurationReservation) ([]*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("group recovery requires owned intent and complete retained claims")
	op := nc.Status.Configuration.Operation
	owners := 0
	for _, owner := range nc.Status.Configuration.Owners {
		if owner.Section != op.Section {
			continue
		}
		if owner.ConfigName != op.ConfigName || owner.ConfigUID != op.ConfigUID {
			return nil, reject
		}
		owners++
	}
	if owners != 1 {
		return nil, reject
	}
	bound, err := ValidateGroupReservationSet(nc, current)
	if err != nil {
		return nil, err
	}
	for _, r := range bound {
		claim := r.Status.Claim
		if claim == nil || claim.OperationID != op.ID || claim.OperationDigest != op.PhysicalGroup.Digest || claim.ClaimedAt.IsZero() || claim.ClaimedAt.After(op.SubmittedAt.Time) || r.Status.Grant == nil || r.Status.Grant.Phase != "Held" || r.Status.Completion != nil || r.Status.ManagerSettledAt != nil || r.Status.PreparationCancelledAt != nil {
			return nil, reject
		}
	}
	return bound, nil
}
