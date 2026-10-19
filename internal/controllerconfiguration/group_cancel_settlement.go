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
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateCancelledGroupMemberSettlement checks retained manager evidence for
// one acknowledged member. It does not establish that all targets have settled.
func ValidateCancelledGroupMemberSettlement(nc *cvk.NetworkController, r *ops.ControllerConfigurationReservation, now time.Time) error {
	if err := ValidateGroupCancellationAcknowledgement(nc, r, now); err != nil {
		return err
	}
	if r.Status.ManagerSettledAt == nil || r.Status.ManagerSettledAt.IsZero() || r.Status.ManagerSettledAt.After(now) || r.Status.ManagerSettledAt.Before(&r.Status.GroupCancellation.AcknowledgedAt) || (r.Status.Grant != nil && r.Status.Grant.Phase != "Released") {
		return errors.New("cancelled group member lacks retained manager settlement")
	}
	return nil
}

// PublishCancelledGroupManagerSettlement retains proof of the exact settled
// maintenance session after cancellation acknowledgement and actual lease
// release. Ungranted members require independent guard and lease-absence evidence:
// absence of a grant is not proof that preparation acquired no lease. This does
// not settle sessions, remove taints, clear journals or mutate leases. Authorize
// must check installed group admission and the manager actor; use uncached reads.
// Ungranted members with no session instead require the exact unguarded Node
// and an idle or absent canonical Lease, observed twice without version changes.
func PublishCancelledGroupManagerSettlement(ctx context.Context, reader client.Reader, writer client.StatusClient, controller *cvk.NetworkController, target *ops.ControllerConfigurationReservation, namespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) error {
	reject := errors.New("cancelled group settlement requires acknowledged cancellation and an exact settled session")
	if reader == nil || writer == nil || target == nil || target.UID == "" || target.ResourceVersion == "" || namespace == "" || authorize == nil || now.IsZero() {
		return reject
	}
	read := func() (*cvk.NetworkController, *ops.ControllerConfigurationReservation, error) {
		nc, current, err := readGroupSettlementObjects(ctx, reader, controller, []*ops.ControllerConfigurationReservation{target})
		if err != nil {
			return nil, nil, err
		}
		if err := ValidateGroupCancellationAcknowledgement(nc, current[0], now); err != nil {
			return nil, nil, err
		}
		r := current[0]
		if r.ResourceVersion != target.ResourceVersion || r.DeletionTimestamp != nil || (r.Status.Grant != nil && r.Status.Grant.Phase != "Released") {
			return nil, nil, reject
		}
		return nc, r, nil
	}
	nc, r, err := read()
	if err != nil {
		return err
	}
	if err := authorize(ctx, r.DeepCopy()); err != nil {
		return err
	}
	nc, r, err = read()
	if err != nil {
		return err
	}
	if r.Status.ManagerSettledAt != nil {
		return ValidateCancelledGroupMemberSettlement(nc, r, now)
	}
	var device cvk.CiscoDevice
	key := client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}
	if err := reader.Get(ctx, key, &device); err != nil {
		return err
	}
	if device.Status.MaintenanceSession == nil {
		if err := readUnstartedGroupCancellation(ctx, reader, r, &device, namespace); err != nil {
			return err
		}
	} else {
		if err := validateSettledConfigurationSession(r, &device); err != nil {
			return err
		}
		s, g := device.Status.MaintenanceSession, r.Status.Grant
		holder, err := Holder(r.Namespace, r)
		canonical := engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
		if err != nil || s.Lease.Namespace != namespace || s.Lease.Name != canonical || s.Lease.UID == "" || s.Lease.Holder != holder || s.RequestedAt.IsZero() || s.AcknowledgedAt == nil || s.AcknowledgedAt.IsZero() || s.AcknowledgedAt.Before(&s.RequestedAt) || r.Status.GroupCancellation.AcknowledgedAt.Before(s.AcknowledgedAt) {
			return reject
		}
		if g != nil && (g.LeaseNamespace != namespace || g.Lease.Name != canonical || g.Lease.UID != s.Lease.UID || g.Holder != holder || g.GrantedAt.Before(s.AcknowledgedAt)) {
			return reject
		}
		if err := readConfigurationLeaseWithoutHolder(ctx, reader, namespace, canonical, holder); err != nil {
			return err
		}
	}
	var fresh cvk.CiscoDevice
	if err := reader.Get(ctx, key, &fresh); err != nil {
		return err
	}
	if fresh.UID != device.UID || fresh.ResourceVersion != device.ResourceVersion {
		return reject
	}
	if _, _, err := read(); err != nil {
		return err
	}
	at := metav1.NewTime(now.UTC().Truncate(time.Second))
	r.Status.ManagerSettledAt = &at
	if err := ValidateCancelledGroupMemberSettlement(nc, r, now); err != nil {
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
	return ValidateCancelledGroupMemberSettlement(nc, &persisted, now)
}
