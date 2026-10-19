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

// ClaimGroupOnce is a staged, no-replay claim coordinator, not a dispatch permit.
// Authorize must check installed group admission/worker compatibility, live
// intent/worker ownership and workload/maintenance policy with uncached reads.
// The writer must be the admitted connector actor. Runtime callers must
// explicitly select and verify the installed group protocol.
//
// Claims are individual resourceVersion CAS writes, not an atomic transaction.
// Any conflict, ambiguous response or validation failure stops the sequence and
// returns no result; already persisted claims remain fenced for recovery. This
// function refuses an existing claim and never reconstructs submission authority.
func ClaimGroupOnce(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, reservations []*ops.ControllerConfigurationReservation, namespace string, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) ([]*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("group claim requires complete versioned authorized evidence")
	if reader == nil || writer == nil || expected == nil || expected.ResourceVersion == "" || authorize == nil || now.IsZero() {
		return nil, reject
	}
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	bound, err := ValidateGroupReservationSet(expected, reservations)
	if err != nil {
		return nil, err
	}
	readController := func() (*cvk.NetworkController, error) {
		var current cvk.NetworkController
		if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &current); err != nil {
			return nil, err
		}
		if current.UID != expected.UID || current.ResourceVersion != expected.ResourceVersion {
			return nil, reject
		}
		return &current, nil
	}
	readSet := func(want []*ops.ControllerConfigurationReservation, exactVersion bool) (*cvk.NetworkController, []*ops.ControllerConfigurationReservation, []*coordination.Lease, error) {
		nc, err := readController()
		if err != nil {
			return nil, nil, nil, err
		}
		current := make([]*ops.ControllerConfigurationReservation, 0, len(want))
		leases := make([]*coordination.Lease, 0, len(want))
		for _, prior := range want {
			if prior.ResourceVersion == "" {
				return nil, nil, nil, reject
			}
			var r ops.ControllerConfigurationReservation
			if err := reader.Get(ctx, client.ObjectKeyFromObject(prior), &r); err != nil {
				return nil, nil, nil, err
			}
			if r.UID != prior.UID || !reflect.DeepEqual(r.Spec, prior.Spec) || (exactVersion && r.ResourceVersion != prior.ResourceVersion) || r.Status.Grant == nil || !reflect.DeepEqual(r.Status.Grant, prior.Status.Grant) || (prior.Status.Claim != nil && !reflect.DeepEqual(r.Status.Claim, prior.Status.Claim)) {
				return nil, nil, nil, reject
			}
			var lease coordination.Lease
			if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
				return nil, nil, nil, err
			}
			current = append(current, &r)
			leases = append(leases, &lease)
		}
		return nc, current, leases, nil
	}
	// Verify before and after authorization, which may itself take time or perform
	// reads. Authorize receives copies and cannot mutate the expected bindings.
	for pass := 0; pass < 2; pass++ {
		nc, current, leases, err := readSet(bound, true)
		if err != nil {
			return nil, err
		}
		if err := ValidateGroupPreparation(nc, current, leases, namespace, observedNow()); err != nil {
			return nil, err
		}
		if pass == 0 {
			if err := authorize(ctx, copyGroupReservations(current)); err != nil {
				return nil, err
			}
		}
	}
	written := make([]*ops.ControllerConfigurationReservation, 0, len(bound))
	for _, prior := range bound {
		nc, err := readController()
		if err != nil {
			return nil, err
		}
		var current ops.ControllerConfigurationReservation
		if err := reader.Get(ctx, client.ObjectKeyFromObject(prior), &current); err != nil {
			return nil, err
		}
		if current.UID != prior.UID || current.ResourceVersion != prior.ResourceVersion || !reflect.DeepEqual(current.Spec, prior.Spec) || current.Status.Claim != nil || current.Status.Completion != nil || current.Status.ManagerSettledAt != nil || current.Status.Grant == nil {
			return nil, reject
		}
		var lease coordination.Lease
		if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: current.Status.Grant.Lease.Name}, &lease); err != nil {
			return nil, err
		}
		if err := validateLeaseGrantEvidence(&current, &lease, namespace, observedNow()); err != nil {
			return nil, err
		}
		if err := ValidateGrantFreshness(&current, observedNow()); err != nil {
			return nil, err
		}
		if err := persistConfigurationClaim(ctx, reader, writer, &current, nc.Status.Configuration.Operation, observedNow()); err != nil {
			return nil, err
		}
		written = append(written, current.DeepCopy())
	}
	nc, claimed, leases, err := readSet(written, false)
	if err != nil {
		return nil, err
	}
	if err := ValidateGroupLiveGrants(nc, claimed, leases, namespace, observedNow()); err != nil {
		return nil, err
	}
	for _, r := range claimed {
		c := r.Status.Claim
		if c == nil || c.OperationID != nc.Status.Configuration.Operation.ID || c.OperationDigest != r.Spec.OperationDigest || c.ClaimedAt.IsZero() || c.ClaimedAt.After(observedNow()) {
			return nil, reject
		}
		if err := ValidateGrantFreshness(r, observedNow()); err != nil {
			return nil, err
		}
	}
	return copyGroupReservations(claimed), nil
}

func copyGroupReservations(in []*ops.ControllerConfigurationReservation) []*ops.ControllerConfigurationReservation {
	out := make([]*ops.ControllerConfigurationReservation, len(in))
	for i := range in {
		out[i] = in[i].DeepCopy()
	}
	return out
}
