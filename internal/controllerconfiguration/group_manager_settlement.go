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

// RetainGroupMemberSettlement resolves the exact complete receipt set before
// using the shared settlement publisher. Callers supply one observed member,
// never an inferred or partial group; all existing publication gates still apply.
func RetainGroupMemberSettlement(ctx context.Context, reader client.Reader, writer client.StatusClient, target *ops.ControllerConfigurationReservation, namespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) error {
	nc, members, err := readGroupMemberCompletionSet(ctx, reader, target, now)
	if err != nil {
		return err
	}
	return PublishGroupManagerSettlement(ctx, reader, writer, nc, members, target, namespace, authorize, now)
}

// ValidateGroupManagerSettlement requires independently verified completion,
// device-worker release and retained manager settlement on EVERY member. It is
// historical evidence validation, not permission to clear a journal or mutate.
func ValidateGroupManagerSettlement(nc *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, now time.Time) error {
	if err := ValidateGroupCompletionReceipts(nc, members, now); err != nil {
		return err
	}
	for _, r := range members {
		if err := validateGroupMemberSettlement(r, now); err != nil {
			return err
		}
	}
	return nil
}
func validateGroupMemberSettlement(r *ops.ControllerConfigurationReservation, now time.Time) error {
	if r == nil || r.Spec.Protocol != GroupReservationProtocol || r.Status.Grant == nil || r.Status.Grant.Phase != "Released" || r.Status.Completion == nil || r.Status.ManagerSettledAt == nil || r.Status.ManagerSettledAt.IsZero() || r.Status.ManagerSettledAt.After(now) || r.Status.ManagerSettledAt.Before(&r.Status.Completion.VerifiedAt) {
		return errors.New("group member lacks retained manager settlement")
	}
	return nil
}

// PublishGroupManagerSettlement records an already-settled exact per-device
// maintenance session. It never releases a Lease, settles a session or clears a
// journal. Authorize must verify installed group admission and the manager actor.
func PublishGroupManagerSettlement(ctx context.Context, reader client.Reader, writer client.StatusClient, controller *cvk.NetworkController, members []*ops.ControllerConfigurationReservation, target *ops.ControllerConfigurationReservation, namespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) error {
	reject := errors.New("group manager settlement requires a released verified exact session")
	if reader == nil || writer == nil || target == nil || target.UID == "" || target.ResourceVersion == "" || namespace == "" || authorize == nil || now.IsZero() {
		return reject
	}
	read := func() (*ops.ControllerConfigurationReservation, error) {
		nc, current, err := readGroupSettlementObjects(ctx, reader, controller, members)
		if err != nil {
			return nil, err
		}
		if err := ValidateGroupCompletionReceipts(nc, current, now); err != nil {
			return nil, err
		}
		for _, r := range current {
			if r.UID != target.UID {
				continue
			}
			if r.ResourceVersion != target.ResourceVersion || r.Name != target.Name || r.Namespace != target.Namespace || !reflect.DeepEqual(r.Spec, target.Spec) || r.DeletionTimestamp != nil || r.Status.Grant.Phase != "Released" {
				return nil, reject
			}
			return r, nil
		}
		return nil, reject
	}
	r, err := read()
	if err != nil {
		return err
	}
	if err := authorize(ctx, r.DeepCopy()); err != nil {
		return err
	}
	r, err = read()
	if err != nil {
		return err
	}
	if r.Status.ManagerSettledAt != nil {
		return validateGroupMemberSettlement(r, now)
	}
	var device cvk.CiscoDevice
	key := client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}
	if err := reader.Get(ctx, key, &device); err != nil {
		return err
	}
	if err := validateSettledConfigurationSession(r, &device); err != nil {
		return err
	}
	s, g := device.Status.MaintenanceSession, r.Status.Grant
	if s.Lease.Namespace != namespace || s.Lease.Namespace != g.LeaseNamespace || s.Lease.Name != g.Lease.Name || s.Lease.UID != g.Lease.UID || s.Lease.Holder != g.Holder || s.RequestedAt.IsZero() || s.AcknowledgedAt == nil || s.AcknowledgedAt.IsZero() || s.AcknowledgedAt.Before(&s.RequestedAt) || g.GrantedAt.Before(s.AcknowledgedAt) {
		return reject
	}
	if err := readReleasedConfigurationLease(ctx, reader, r, namespace); err != nil {
		return err
	}
	var fresh cvk.CiscoDevice
	if err := reader.Get(ctx, key, &fresh); err != nil {
		return err
	}
	if fresh.UID != device.UID || fresh.ResourceVersion != device.ResourceVersion {
		return reject
	}
	at := metav1.NewTime(now.UTC().Truncate(time.Second))
	r.Status.ManagerSettledAt = &at
	if err := validateGroupMemberSettlement(r, now); err != nil {
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
	return validateGroupMemberSettlement(&persisted, now)
}
