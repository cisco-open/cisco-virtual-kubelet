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

// ReadGroupClaimRetention validates retained operation evidence for keeping an
// existing maintenance fence. It does not require current desired intent or
// worker readiness, and never authorizes dispatch, grant refresh or release.
func ReadGroupClaimRetention(ctx context.Context, reader client.Reader, member *ops.ControllerConfigurationReservation, lease *coordination.Lease, namespace string, now time.Time) error {
	reject := errors.New("group claim retention lacks exact journal/member/lease evidence")
	if reader == nil || member == nil || member.UID == "" || member.Spec.Protocol != GroupReservationProtocol || lease == nil || now.IsZero() {
		return reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: member.Namespace, Name: member.Spec.Controller.Name}, &nc); err != nil {
		return err
	}
	if string(nc.UID) != member.Spec.Controller.UID || nc.Status.Configuration == nil || ValidateGroupJournalIdentity(nc.Status.Configuration.Operation) != nil {
		return reject
	}
	op := nc.Status.Configuration.Operation
	targets, err := CanonicalGroupTargets(op.PhysicalGroup.Targets)
	if err != nil {
		return err
	}
	rows := make([]*ops.ControllerConfigurationReservation, len(targets))
	found := false
	for i, target := range targets {
		var row ops.ControllerConfigurationReservation
		key := client.ObjectKey{Namespace: nc.Namespace, Name: groupMemberReservationName(string(nc.UID), op.ID, op.PhysicalGroup.Digest, target.PhysicalIdentity)}
		if err := reader.Get(ctx, key, &row); err != nil {
			return err
		}
		if row.UID == member.UID && row.Name == member.Name {
			if row.ResourceVersion != member.ResourceVersion || !reflect.DeepEqual(row.Spec, member.Spec) || !reflect.DeepEqual(row.Status, member.Status) {
				return reject
			}
			found = true
		}
		rows[i] = &row
	}
	if !found {
		return reject
	}
	if _, err := ValidateGroupReservationSet(&nc, rows); err != nil {
		return err
	}
	claim := member.Status.Claim
	if claim == nil || claim.OperationID != op.ID || claim.OperationDigest != op.PhysicalGroup.Digest || claim.ClaimedAt.IsZero() || claim.ClaimedAt.After(now) || (op.SubmittedAt != nil && op.SubmittedAt.Before(&claim.ClaimedAt)) || member.Status.ManagerSettledAt != nil {
		return reject
	}
	if err := validateLeaseGrantEvidence(member, lease, namespace, now); err != nil {
		return err
	}
	// Changes during collection require a fresh observation, never a new grant.
	objects := []client.Object{&nc, lease}
	for _, row := range rows {
		objects = append(objects, row)
	}
	for _, old := range objects {
		fresh := old.DeepCopyObject().(client.Object)
		if err := reader.Get(ctx, client.ObjectKeyFromObject(old), fresh); err != nil {
			return err
		}
		if fresh.GetUID() != old.GetUID() || fresh.GetResourceVersion() != old.GetResourceVersion() {
			return reject
		}
	}
	return nil
}
