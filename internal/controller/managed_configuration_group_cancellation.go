// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controller

import (
	"context"
	"errors"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Staged group recovery for preparation cancelled before a session was ever
// acknowledged. It reuses the v1 manager's idle lease/node proof and never
// manufactures a session. Runtime callers remain disabled until group admission
// and worker rollout are installed; authorize must verify that contract and the
// manager actor before any status write. Grants/claims require another path.
func (r *CiscoDeviceReconciler) settleUnacknowledgedGroupCancellation(ctx context.Context, expected *ops.ControllerConfigurationReservation, authorize func(context.Context, *ops.ControllerConfigurationReservation) error) error {
	reject := errors.New("group cancellation lacks unacknowledged cleanup authority")
	if r.APIReader == nil || r.Client == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || authorize == nil {
		return reject
	}
	now := r.now()
	read := func() (*cvk.NetworkController, *ops.ControllerConfigurationReservation, error) {
		var row ops.ControllerConfigurationReservation
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(expected), &row); err != nil {
			return nil, nil, err
		}
		if row.UID != expected.UID || row.ResourceVersion != expected.ResourceVersion || !reflect.DeepEqual(row.Spec, expected.Spec) || row.DeletionTimestamp != nil || row.Status.Grant != nil || row.Status.Claim != nil {
			return nil, nil, reject
		}
		var nc cvk.NetworkController
		if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: row.Namespace, Name: row.Spec.Controller.Name}, &nc); err != nil {
			return nil, nil, err
		}
		if err := contract.ValidateGroupCancellationAcknowledgement(&nc, &row, now); err != nil {
			return nil, nil, err
		}
		return &nc, &row, nil
	}
	_, row, err := read()
	if err != nil {
		return err
	}
	if err := authorize(ctx, row.DeepCopy()); err != nil {
		return err
	}
	nc, row, err := read()
	if err != nil {
		return err
	}
	if row.Status.ManagerSettledAt != nil {
		return contract.ValidateCancelledGroupMemberSettlement(nc, row, now)
	}
	var device cvk.CiscoDevice
	if err := r.APIReader.Get(ctx, client.ObjectKey{Namespace: row.Namespace, Name: row.Spec.Device.Name}, &device); err != nil {
		return err
	}
	if string(device.UID) != row.Spec.Device.UID {
		return reject
	}
	clean, err := r.readUnacknowledgedConfigurationCleanup(ctx, &device, row)
	if err != nil {
		return err
	}
	if !clean {
		return reject
	}
	stamp := metav1.NewTime(now.UTC().Truncate(time.Second))
	row.Status.ManagerSettledAt = &stamp
	if err := contract.ValidateCancelledGroupMemberSettlement(nc, row, now); err != nil {
		return err
	}
	intended := row.DeepCopy()
	if err := r.Status().Update(ctx, row); err != nil {
		return err
	}
	var persisted ops.ControllerConfigurationReservation
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(intended), &persisted); err != nil {
		return err
	}
	if persisted.UID != intended.UID || !reflect.DeepEqual(persisted.Spec, intended.Spec) || !equality.Semantic.DeepEqual(persisted.Status, intended.Status) {
		return reject
	}
	return contract.ValidateCancelledGroupMemberSettlement(nc, &persisted, now)
}
