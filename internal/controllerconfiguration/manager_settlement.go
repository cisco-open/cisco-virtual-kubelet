// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// ValidateManagerSettlement checks retained manager evidence after the device
// has advanced to another session. Actor-specific admission protects the marker;
// neither expiry nor device-worker Released status alone supplies this proof.
func ValidateManagerSettlement(r *ops.ControllerConfigurationReservation, now time.Time) error {
	reject := errors.New("configuration manager settlement evidence is incomplete")
	if r == nil || r.Spec.Protocol != Protocol || r.UID == "" || r.Status.ManagerSettledAt == nil || r.Status.ManagerSettledAt.IsZero() || r.Status.ManagerSettledAt.After(now) {
		return reject
	}
	if r.Status.PreparationCancelledAt != nil {
		if err := ValidatePreparationCancellation(r, now); err != nil {
			return err
		}
		if r.Status.ManagerSettledAt.Before(r.Status.PreparationCancelledAt) {
			return reject
		}
		return nil
	}
	if r.Status.Grant == nil || r.Status.Grant.Phase != "Released" || r.Status.Grant.GrantedAt.IsZero() || r.Status.ManagerSettledAt.Before(&r.Status.Grant.GrantedAt) || RequiresQuarantine(r, now) {
		return reject
	}
	if r.Status.Completion != nil && r.Status.ManagerSettledAt.Before(&r.Status.Completion.VerifiedAt) {
		return reject
	}
	return nil
}

// PublishManagerSettlement retains proof of an already-persisted exact manager
// session. Call only from the admitted manager with uncached reads. It does not
// settle a session, release a Lease, clear a journal or delete a reservation.
func PublishManagerSettlement(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *ops.ControllerConfigurationReservation, now time.Time) error {
	reject := errors.New("configuration session is not settled for this reservation")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || now.IsZero() {
		return reject
	}
	var r ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &r); err != nil {
		return err
	}
	if r.UID != expected.UID || r.ResourceVersion != expected.ResourceVersion || !reflect.DeepEqual(r.Spec, expected.Spec) || r.DeletionTimestamp != nil || r.Spec.Protocol != Protocol {
		return reject
	}
	if r.Status.ManagerSettledAt != nil {
		return ValidateManagerSettlement(&r, now)
	}
	g := r.Status.Grant
	cancelled := ValidatePreparationCancellation(&r, now) == nil
	if !cancelled && (g == nil || g.Phase != "Released" || RequiresQuarantine(&r, now)) {
		return reject
	}
	var device cvk.CiscoDevice
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}, &device); err != nil {
		return err
	}
	s := device.Status.MaintenanceSession
	if err := validateSettledConfigurationSession(&r, &device); err != nil {
		return err
	}
	if cancelled {
		holder, err := Holder(r.Namespace, &r)
		if err != nil || s.AcknowledgedAt == nil || s.AcknowledgedAt.IsZero() || s.AcknowledgedAt.Before(&s.RequestedAt) || r.Status.PreparationCancelledAt.Before(s.AcknowledgedAt) || s.Lease.Holder != holder || s.Lease.UID == "" || s.Lease.Namespace == "" || s.Lease.Name != engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily) {
			return reject
		}
	} else if s.Lease.Namespace != g.LeaseNamespace || s.Lease.Name != g.Lease.Name || s.Lease.UID != g.Lease.UID || s.Lease.Holder != g.Holder {
		return reject
	}
	if r.Status.Claim != nil {
		var nc cvk.NetworkController
		if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}, &nc); err != nil {
			return err
		}
		if err := ValidateCompletionReceipt(&r, &nc, now); err != nil {
			return err
		}
	}
	var fresh cvk.CiscoDevice
	if err := reader.Get(ctx, client.ObjectKeyFromObject(&device), &fresh); err != nil {
		return err
	}
	if fresh.UID != device.UID || fresh.ResourceVersion != device.ResourceVersion {
		return reject
	}
	r.Status.ManagerSettledAt = &metav1.Time{Time: now}
	if err := ValidateManagerSettlement(&r, now); err != nil {
		return err
	}
	if err := writer.Status().Update(ctx, &r); err != nil {
		return err
	}
	var persisted ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, client.ObjectKeyFromObject(&r), &persisted); err != nil {
		return err
	}
	if persisted.UID != r.UID || !reflect.DeepEqual(persisted.Spec, r.Spec) || persisted.Status.ManagerSettledAt == nil || !persisted.Status.ManagerSettledAt.Equal(r.Status.ManagerSettledAt) {
		return reject
	}
	return ValidateManagerSettlement(&persisted, now)
}

func validateSettledConfigurationSession(r *ops.ControllerConfigurationReservation, device *cvk.CiscoDevice) error {
	if r == nil || device == nil {
		return errors.New("configuration maintenance session unavailable")
	}
	s := device.Status.MaintenanceSession
	if string(device.UID) != r.Spec.Device.UID || s == nil || s.Phase != cvk.DeviceMaintenanceSessionSettled || s.ProtocolVersion != cvk.DeviceMaintenanceProtocolConfigurationV1 || s.Purpose != cvk.DeviceMaintenancePurposeConfigurationMutation || s.DeviceUID != r.Spec.Device.UID || s.NodeName != r.Spec.Node.Name || s.NodeUID != r.Spec.Node.UID || s.ControlRevision != r.Spec.ConfigGeneration || s.Operation.Namespace != r.Namespace || s.Operation.Name != r.Name || s.Operation.UID != string(r.UID) {
		return errors.New("configuration maintenance session does not match reservation")
	}
	return nil
}
