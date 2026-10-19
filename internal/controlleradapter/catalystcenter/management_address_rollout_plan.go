// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	corev1 "k8s.io/api/core/v1"
)

// Plan a declaration change only after the native operation's correlated
// completion and manager settlement. This does not patch a device, authorize
// rollout, remove scheduling guards or claim that the new worker is ready.
func planManagementAddressDeclaration(ref configurationDeviceReference, nc *cvk.NetworkController, reservation *ops.ControllerConfigurationReservation, device *cvk.CiscoDevice, node *corev1.Node, now time.Time) (*cvk.CiscoDevice, error) {
	if nc == nil || reservation == nil || device == nil || node == nil || now.IsZero() || nc.UID == "" || string(nc.UID) != reservation.Spec.Controller.UID || nc.Namespace != reservation.Namespace || nc.Spec.Type != TypeName || nc.Spec.Paused || !nc.DeletionTimestamp.IsZero() || nc.Status.Configuration == nil {
		return nil, errConfigurationStale
	}
	if err := contract.ValidateCompletionReceipt(reservation, nc, now); err != nil {
		return nil, err
	}
	if err := contract.ValidateManagerSettlement(reservation, now); err != nil {
		return nil, err
	}
	var verified *cvk.NetworkControllerConfigurationOperation
	for i := range nc.Status.Configuration.ActionApplications {
		operation := &nc.Status.Configuration.ActionApplications[i].Operation
		if operation.ID == reservation.Spec.OperationID {
			if verified != nil {
				return nil, errConfigurationStale
			}
			verified = operation
		}
	}
	baseline, err := retainedManagementAddressBaseline(nc.Namespace, ref, verified, reservation)
	if err != nil {
		return nil, err
	}
	if device.Name != reservation.Spec.Device.Name || string(device.UID) != reservation.Spec.Device.UID || device.Generation != reservation.Spec.DeviceGeneration || node.Name != reservation.Spec.Node.Name || string(node.UID) != reservation.Spec.Node.UID {
		return nil, errConfigurationStale
	}
	if _, err := configurationManagedDevice(nc.Namespace, baseline.device, []cvk.CiscoDevice{*device}); err != nil {
		return nil, err
	}
	if err := configurationManagedNode(device, node); err != nil {
		return nil, err
	}
	if device.Status.MaintenanceSession != nil && device.Status.MaintenanceSession.Phase != cvk.DeviceMaintenanceSessionSettled {
		return nil, errConfigurationBusy
	}
	proposed := device.DeepCopy()
	proposed.Spec.Address = baseline.after
	return proposed, nil
}
