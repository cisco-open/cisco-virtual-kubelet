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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// EnsureGroupReservations is the staged manager creation boundary. Complete
// existing evidence is returned unchanged for recovery. Creating missing members
// requires full current candidates and explicit installed group admission/worker
// authorization. Partial writes are retained, never rolled back or retried here.
func EnsureGroupReservations(ctx context.Context, reader client.Reader, writer client.Writer, expected *cvk.NetworkController, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) ([]*ops.ControllerConfigurationReservation, error) {
	return ensureGroupReservations(ctx, reader, writer, expected, authorize, now, false)
}

// EnsureCancelledGroupReservations retains missing members under a cancellation
// fence rather than interpreting absence as settlement. Existing members retain
// their UID, grants and claims. Workers must acknowledge cancellation and the
// manager must independently settle every member before journal cleanup.
func EnsureCancelledGroupReservations(ctx context.Context, reader client.Reader, writer client.Writer, expected *cvk.NetworkController, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) ([]*ops.ControllerConfigurationReservation, error) {
	return ensureGroupReservations(ctx, reader, writer, expected, authorize, now, true)
}

func ensureGroupReservations(ctx context.Context, reader client.Reader, writer client.Writer, expected *cvk.NetworkController, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time, cancelling bool) ([]*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("group reservation creation lacks complete current authority")
	if reader == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || now.IsZero() {
		return nil, reject
	}
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return nil, err
	}
	if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion || nc.Status.Configuration == nil || ValidateGroupJournalIdentity(nc.Status.Configuration.Operation) != nil {
		return nil, reject
	}
	op := nc.Status.Configuration.Operation
	if cancelling && ValidateGroupCancellationFence(&nc, now) != nil {
		return nil, reject
	}
	targets, err := CanonicalGroupTargets(op.PhysicalGroup.Targets)
	if err != nil {
		return nil, err
	}
	readExisting := func() ([]*ops.ControllerConfigurationReservation, error) {
		rows := make([]*ops.ControllerConfigurationReservation, len(targets))
		for i, target := range targets {
			var r ops.ControllerConfigurationReservation
			key := client.ObjectKey{Namespace: nc.Namespace, Name: groupMemberReservationName(string(nc.UID), op.ID, op.PhysicalGroup.Digest, target.PhysicalIdentity)}
			if err := reader.Get(ctx, key, &r); err != nil {
				if apierrors.IsNotFound(err) {
					continue
				}
				return nil, err
			}
			if validateGroupReservationMember(&nc, &r, target) != nil {
				return nil, reject
			}
			rows[i] = &r
		}
		return rows, nil
	}
	rows, err := readExisting()
	if err != nil {
		return nil, err
	}
	complete := true
	for _, r := range rows {
		if r == nil {
			complete = false
		}
	}
	if complete {
		return ValidateGroupReservationSet(&nc, rows)
	}
	if writer == nil || authorize == nil || (!cancelling && (op.Phase != "Prepared" || op.CancellationRequestedAt != nil)) || op.TaskID != "" || op.SubmittedAt != nil {
		return nil, reject
	}
	readCandidates := ReadGroupReservationCandidates
	if cancelling {
		readCandidates = ReadCancelledGroupReservationCandidates
	}
	candidates, err := readCandidates(ctx, reader, &nc, observedNow())
	if err != nil {
		return nil, err
	}
	validateExisting := func(current []*ops.ControllerConfigurationReservation) error {
		for i, r := range current {
			if r == nil {
				continue
			}
			if cancelling {
				if !reflect.DeepEqual(r.Spec, candidates[i].Spec) || ValidateGroupCancellationMember(&nc, r, observedNow()) != nil {
					return reject
				}
				continue
			}
			if !reflect.DeepEqual(r.Spec, candidates[i].Spec) || r.Status.Claim != nil || r.Status.Completion != nil || r.Status.ManagerSettledAt != nil || r.Status.PreparationCancelledAt != nil || r.Status.GroupCancellation != nil || (r.Status.Grant != nil && r.Status.Grant.Phase != "Held") {
				return reject
			}
		}
		return nil
	}
	if err := validateExisting(rows); err != nil {
		return nil, err
	}
	if err := authorize(ctx, copyGroupReservations(candidates)); err != nil {
		return nil, err
	}
	for i := range candidates {
		fresh, err := readCandidates(ctx, reader, &nc, observedNow())
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(fresh, candidates) {
			return nil, reject
		}
		for _, r := range fresh {
			if err := ReadNetworkMutationRole(ctx, reader, r); err != nil {
				return nil, err
			}
		}
		rows, err = readExisting()
		if err != nil {
			return nil, err
		}
		if err := validateExisting(rows); err != nil {
			return nil, err
		}
		if rows[i] != nil {
			continue
		}
		candidate := candidates[i].DeepCopy()
		if err := writer.Create(ctx, candidate); err != nil {
			return nil, err
		}
		if candidate.UID == "" {
			return nil, reject
		}
		var stored ops.ControllerConfigurationReservation
		if err := reader.Get(ctx, client.ObjectKeyFromObject(candidate), &stored); err != nil {
			return nil, err
		}
		if stored.UID != candidate.UID || !reflect.DeepEqual(stored.Spec, candidate.Spec) || validateGroupReservationMember(&nc, &stored, targets[i]) != nil {
			return nil, reject
		}
	}
	fresh, err := readCandidates(ctx, reader, &nc, observedNow())
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(fresh, candidates) {
		return nil, reject
	}
	rows, err = readExisting()
	if err != nil {
		return nil, err
	}
	return ValidateGroupReservationSet(&nc, rows)
}
