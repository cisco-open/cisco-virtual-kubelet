// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"errors"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/types"
)

// ValidateGroupLiveGrants validates the complete journal-bound reservation and
// canonical Lease set at one observation time. LeaseNamespace is trusted worker
// configuration, not a namespace taken from a grant. This is evidence validation
// only; callers must independently verify current objects, intent and actors.
// It cannot authorize dispatch, renewal or release, including after a restart.
func ValidateGroupLiveGrants(nc *cvk.NetworkController, reservations []*ops.ControllerConfigurationReservation, leases []*coordination.Lease, leaseNamespace string, now time.Time) error {
	bound, err := ValidateGroupReservationSet(nc, reservations)
	if err != nil {
		return err
	}
	return validateGroupLeaseSet(bound, leases, leaseNamespace, now)
}

func validateGroupLeaseSet(bound []*ops.ControllerConfigurationReservation, leases []*coordination.Lease, namespace string, now time.Time) error {
	reject := errors.New("configuration group requires a complete live lease set")
	if len(bound) == 0 || len(leases) != len(bound) || namespace == "" || now.IsZero() {
		return reject
	}
	indexed := make(map[types.NamespacedName]*coordination.Lease, len(leases))
	uids := map[types.UID]bool{}
	for _, lease := range leases {
		if lease == nil || lease.Namespace != namespace || lease.Name == "" || lease.UID == "" || uids[lease.UID] {
			return reject
		}
		key := types.NamespacedName{Namespace: lease.Namespace, Name: lease.Name}
		if indexed[key] != nil {
			return reject
		}
		indexed[key] = lease
		uids[lease.UID] = true
	}
	used := map[types.NamespacedName]bool{}
	for _, r := range bound {
		if r.Status.Grant == nil || r.Status.ManagerSettledAt != nil || r.Status.Completion != nil {
			return reject
		}
		key := types.NamespacedName{Namespace: namespace, Name: r.Status.Grant.Lease.Name}
		if used[key] {
			return reject
		}
		used[key] = true
		if err := validateLeaseGrantEvidence(r, indexed[key], namespace, now); err != nil {
			return err
		}
	}
	return nil
}

// ValidateGroupPreparation additionally requires fresh native preparation on
// every unclaimed member. A renewed Lease never refreshes preparation age.
// Success is not a permit: claims and one-shot journal CAS remain mandatory.
func ValidateGroupPreparation(nc *cvk.NetworkController, reservations []*ops.ControllerConfigurationReservation, leases []*coordination.Lease, leaseNamespace string, now time.Time) error {
	bound, err := ValidateGroupReservationSet(nc, reservations)
	if err != nil {
		return err
	}
	op := nc.Status.Configuration.Operation
	if nc.DeletionTimestamp != nil || nc.Spec.Paused || nc.Generation != op.ControllerGeneration || op.Phase != "Prepared" || op.TaskID != "" || op.SubmittedAt != nil {
		return errors.New("configuration group is not awaiting initial claims")
	}
	if err := validateGroupLeaseSet(bound, leases, leaseNamespace, now); err != nil {
		return err
	}
	for _, r := range bound {
		if r.Status.Claim != nil {
			return errors.New("configuration group preparation is already claimed")
		}
		if err := ValidateGrantFreshness(r, now); err != nil {
			return err
		}
	}
	return nil
}
