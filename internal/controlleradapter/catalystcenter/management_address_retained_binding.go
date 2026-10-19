// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	coordination "k8s.io/api/coordination/v1"
	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Combine historical endpoint/live worker evidence with the shared retained
// claim/lease observer. This still grants no dispatch or settlement authority.
func readRetainedManagementAddressLease(ctx context.Context, reader ctrlclient.Reader, namespace string, ref configurationDeviceReference, op *cvk.NetworkControllerConfigurationOperation, expected *ops.ControllerConfigurationReservation, leaseNamespace string, now time.Time) (*managementAddressChange, *cvk.CiscoDevice, *corev1.Node, error) {
	baseline, device, node, err := readRetainedManagementAddressBinding(ctx, reader, namespace, ref, op, expected, now)
	if err != nil {
		return nil, nil, nil, err
	}
	var current ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, ctrlclient.ObjectKeyFromObject(expected), &current); err != nil {
		return nil, nil, nil, err
	}
	if current.UID != expected.UID || !reflect.DeepEqual(current.Spec, expected.Spec) || current.Status.Grant == nil {
		return nil, nil, nil, errConfigurationStale
	}
	var lease coordination.Lease
	if err := reader.Get(ctx, ctrlclient.ObjectKey{Namespace: leaseNamespace, Name: current.Status.Grant.Lease.Name}, &lease); err != nil {
		return nil, nil, nil, err
	}
	if err := contract.ReadClaimRetention(ctx, reader, &current, &lease, leaseNamespace, now); err != nil {
		return nil, nil, nil, err
	}
	// The supplied operation must still be the exact live journal, not merely
	// an operation with the same digest but another phase or task receipt.
	var nc cvk.NetworkController
	if err := reader.Get(ctx, ctrlclient.ObjectKey{Namespace: current.Namespace, Name: current.Spec.Controller.Name}, &nc); err != nil {
		return nil, nil, nil, err
	}
	if nc.Status.Configuration == nil || !reflect.DeepEqual(nc.Status.Configuration.Operation, op) {
		return nil, nil, nil, errConfigurationStale
	}
	return baseline, device, node, nil
}

// Validate the retained, admitted old-endpoint view explicitly. It is not a
// rewritten current controller inventory record. Live common device/Node/worker
// checks remain unchanged; source ownership, held lease and destination proof
// are additional obligations of the future runtime recovery bridge.
func readRetainedManagementAddressBinding(ctx context.Context, reader ctrlclient.Reader, namespace string, ref configurationDeviceReference, op *cvk.NetworkControllerConfigurationOperation, expected *ops.ControllerConfigurationReservation, now time.Time) (*managementAddressChange, *cvk.CiscoDevice, *corev1.Node, error) {
	if reader == nil || expected == nil || now.IsZero() {
		return nil, nil, nil, errConfigurationDeviceAdmission
	}
	var reservation ops.ControllerConfigurationReservation
	key := ctrlclient.ObjectKeyFromObject(expected)
	if err := reader.Get(ctx, key, &reservation); err != nil {
		return nil, nil, nil, err
	}
	if reservation.UID != expected.UID || !reflect.DeepEqual(reservation.Spec, expected.Spec) {
		return nil, nil, nil, errConfigurationStale
	}
	baseline, err := retainedManagementAddressBaseline(namespace, ref, op, &reservation)
	if err != nil {
		return nil, nil, nil, err
	}
	device, node, err := resolveConfigurationManagedBinding(ctx, reader, namespace, baseline.device)
	if err != nil {
		return nil, nil, nil, err
	}
	if device.Name != reservation.Spec.Device.Name || string(device.UID) != reservation.Spec.Device.UID || node.Name != reservation.Spec.Node.Name || string(node.UID) != reservation.Spec.Node.UID {
		return nil, nil, nil, errConfigurationStale
	}
	if err := contract.ReadDeviceWorkerBinding(ctx, reader, &reservation, now); err != nil {
		return nil, nil, nil, err
	}
	var current ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, key, &current); err != nil {
		return nil, nil, nil, err
	}
	if current.UID != reservation.UID || !reflect.DeepEqual(current.Spec, reservation.Spec) || !current.DeletionTimestamp.IsZero() {
		return nil, nil, nil, errConfigurationStale
	}
	// Worker reads must not leave us returning an earlier device/Node view.
	// Repeat the shared resolver, including global alias detection, and require
	// the same snapshots before returning any retained binding evidence.
	freshDevice, freshNode, err := resolveConfigurationManagedBinding(ctx, reader, namespace, baseline.device)
	if err != nil {
		return nil, nil, nil, err
	}
	if freshDevice.UID != device.UID || freshDevice.ResourceVersion != device.ResourceVersion || freshNode.UID != node.UID || freshNode.ResourceVersion != node.ResourceVersion {
		return nil, nil, nil, errConfigurationStale
	}
	return baseline, freshDevice, freshNode, nil
}
