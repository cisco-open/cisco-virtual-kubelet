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

// ReadGroupMemberPreparation validates a current ungranted member against the
// complete live Prepared group. It grants no lease or mutation authority.
func ReadGroupMemberPreparation(ctx context.Context, reader client.Reader, member *ops.ControllerConfigurationReservation, now time.Time) error {
	if reader == nil || member == nil || member.UID == "" || member.ResourceVersion == "" || member.Spec.Protocol != GroupReservationProtocol {
		return errors.New("group member preparation identity unavailable")
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: member.Namespace, Name: member.Spec.Controller.Name}, &nc); err != nil {
		return err
	}
	if string(nc.UID) != member.Spec.Controller.UID || nc.Generation != member.Spec.ControllerGeneration {
		return errors.New("group member controller changed")
	}
	return readPreparedGroupForGrant(ctx, reader, &nc, member, now)
}

func readPreparedGroupForGrant(ctx context.Context, reader client.Reader, nc *cvk.NetworkController, member *ops.ControllerConfigurationReservation, now time.Time) error {
	return readPreparedGroupMember(ctx, reader, nc, member, false, now)
}

// ReadGroupHeldMember validates a still-unclaimed grant against complete live
// Prepared membership and its canonical lease. It does not renew or acquire a
// lease, publish a grant or authorize native work. Expired grants cannot revive.
func ReadGroupHeldMember(ctx context.Context, reader client.Reader, member *ops.ControllerConfigurationReservation, namespace string, now time.Time) error {
	if reader == nil || member == nil || member.Spec.Protocol != GroupReservationProtocol || member.Status.Grant == nil || ValidateGrantFreshness(member, now) != nil {
		return errors.New("group held member authority unavailable")
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: member.Namespace, Name: member.Spec.Controller.Name}, &nc); err != nil {
		return err
	}
	if err := readPreparedGroupMember(ctx, reader, &nc, member, true, now); err != nil {
		return err
	}
	var lease coordination.Lease
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: member.Status.Grant.Lease.Name}, &lease); err != nil {
		return err
	}
	return validateLeaseGrantEvidence(member, &lease, namespace, now)
}

func readPreparedGroupMember(ctx context.Context, reader client.Reader, nc *cvk.NetworkController, member *ops.ControllerConfigurationReservation, held bool, now time.Time) error {
	reject := errors.New("group grant requires a complete unclaimed Prepared group")
	candidates, err := ReadGroupReservationCandidates(ctx, reader, nc, now)
	if err != nil {
		return err
	}
	rows := make([]*ops.ControllerConfigurationReservation, len(candidates))
	found := false
	for i, candidate := range candidates {
		var r ops.ControllerConfigurationReservation
		if err := reader.Get(ctx, client.ObjectKeyFromObject(candidate), &r); err != nil {
			return err
		}
		if !reflect.DeepEqual(r.Spec, candidate.Spec) || r.Status.Claim != nil || r.Status.Completion != nil || r.Status.PreparationCancelledAt != nil || r.Status.GroupCancellation != nil || r.Status.ManagerSettledAt != nil || (r.Status.Grant != nil && r.Status.Grant.Phase != "Held") {
			return reject
		}
		if r.UID == member.UID && r.Name == member.Name {
			if !reflect.DeepEqual(r.Spec, member.Spec) || r.ResourceVersion != member.ResourceVersion || (!held && r.Status.Grant != nil) || (held && (r.Status.Grant == nil || !reflect.DeepEqual(r.Status.Grant, member.Status.Grant))) {
				return reject
			}
			found = true
		}
		rows[i] = &r
	}
	if !found {
		return reject
	}
	_, err = ValidateGroupReservationSet(nc, rows)
	return err
}
