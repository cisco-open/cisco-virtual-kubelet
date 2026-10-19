// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controller

import (
	"context"
	"fmt"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	"github.com/cisco/virtual-kubelet-cisco/internal/managedprotocol"
	coordination "k8s.io/api/coordination/v1"
	core "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Run only after successful Node projection and maintenance status persistence.
// A cancellation before acknowledgement has no session to settle. Prove the
// absence of remaining maintenance authority instead; never manufacture one.
func (r *CiscoDeviceReconciler) settleUnacknowledgedConfigurationCancellations(ctx context.Context, expected *cvk.CiscoDevice) error {
	if r.APIReader == nil {
		return fmt.Errorf("configuration cancellation settlement requires uncached reads")
	}
	var rows ops.ControllerConfigurationReservationList
	if err := r.APIReader.List(ctx, &rows, client.InNamespace(expected.Namespace)); err != nil {
		return err
	}
	for i := range rows.Items {
		row := &rows.Items[i]
		if row.Spec.Device.Name != expected.Name || row.Spec.Device.UID != string(expected.UID) || row.Status.PreparationCancelledAt == nil || row.Status.ManagerSettledAt != nil {
			continue
		}
		if err := contract.ValidatePreparationCancellation(row, r.now()); err != nil {
			return err
		}
		if row.DeletionTimestamp != nil {
			continue
		}
		clean, err := r.readUnacknowledgedConfigurationCleanup(ctx, expected, row)
		if err != nil {
			return err
		}
		if !clean {
			continue
		}
		row.Status.ManagerSettledAt = &metav1.Time{Time: r.now()}
		if err := contract.ValidateManagerSettlement(row, r.now()); err != nil {
			return err
		}
		if err := r.Status().Update(ctx, row); err != nil {
			return err
		}
		var persisted ops.ControllerConfigurationReservation
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(row), &persisted); err != nil {
			return err
		}
		if persisted.UID != row.UID || persisted.Status.ManagerSettledAt == nil || !persisted.Status.ManagerSettledAt.Equal(row.Status.ManagerSettledAt) {
			return fmt.Errorf("configuration cancellation settlement was not persisted")
		}
		if err := contract.ValidateManagerSettlement(&persisted, r.now()); err != nil {
			return err
		}
	}
	return nil
}

// Shared read-only proof for cancellation before session acknowledgement.
// The caller must first validate its protocol-specific terminal cancellation.
// An acknowledged session must use the exact-session settlement path instead.
func (r *CiscoDeviceReconciler) readUnacknowledgedConfigurationCleanup(ctx context.Context, expected *cvk.CiscoDevice, row *ops.ControllerConfigurationReservation) (bool, error) {
	var device cvk.CiscoDevice
	if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(expected), &device); err != nil {
		return false, err
	}
	if device.UID != expected.UID || device.DeletionTimestamp != nil {
		return false, nil
	}
	if s := device.Status.MaintenanceSession; s != nil {
		// Exact acknowledged cancellations use the session settlement path. An
		// unrelated active session must be allowed to finish without interference.
		if s.Phase != cvk.DeviceMaintenanceSessionSettled || s.Operation.UID == string(row.UID) {
			return false, nil
		}
	}
	var node core.Node
	if err := r.APIReader.Get(ctx, client.ObjectKey{Name: row.Spec.Node.Name}, &node); err != nil {
		return false, err
	}
	if string(node.UID) != row.Spec.Node.UID || node.DeletionTimestamp != nil || node.Annotations[managedprotocol.AnnotationDeviceUID] != string(device.UID) || node.Annotations[managedprotocol.AnnotationDeviceName] != device.Name || node.Annotations[managedprotocol.AnnotationDeviceNamespace] != device.Namespace {
		return false, nil
	}
	guarded := false
	for _, taint := range node.Spec.Taints {
		if taint.Key == maintenanceGuardTaint().Key {
			guarded = true
		}
	}
	if guarded {
		return false, nil
	}
	namespace := r.LeaseNamespace
	if namespace == "" {
		namespace = device.Namespace
	}
	var lease coordination.Lease
	key := client.ObjectKey{Namespace: namespace, Name: engine.LeaseName(devicecoordination.DeviceKey(device.Namespace, device.Name), devicecoordination.MutationLeaseFamily)}
	if err := r.APIReader.Get(ctx, key, &lease); err != nil {
		return false, err
	}
	if lease.UID == "" || lease.DeletionTimestamp != nil || (lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "") || hasMaintenanceRequestAnnotations(lease.Annotations) {
		return false, nil
	}
	annotations, labels := managedMutationLeaseMetadata(&device, node.Name, string(node.UID), managedNetworkWorkerUsername(&node))
	if err := validateManagedMutationLeaseMetadata(&lease, annotations, labels); err != nil {
		return false, err
	}
	if err := validateManagedMutationLeaseSpec(&lease.Spec, ""); err != nil {
		return false, err
	}
	// Refuse changed observations. The status CAS also protects against a
	// competing reservation update, while admission makes cancellation terminal.
	for _, object := range []client.Object{&device, &node, &lease} {
		fresh := object.DeepCopyObject().(client.Object)
		if err := r.APIReader.Get(ctx, client.ObjectKeyFromObject(object), fresh); err != nil {
			return false, err
		}
		if fresh.GetUID() != object.GetUID() || fresh.GetResourceVersion() != object.GetResourceVersion() {
			return false, fmt.Errorf("configuration cancellation observations changed")
		}
	}
	return true, nil
}

func configurationReservationDeviceRequests(_ context.Context, object client.Object) []ctrl.Request {
	row, ok := object.(*ops.ControllerConfigurationReservation)
	if !ok || row.Namespace == "" || row.Spec.Device.Name == "" {
		return nil
	}
	return []ctrl.Request{{NamespacedName: client.ObjectKey{Namespace: row.Namespace, Name: row.Spec.Device.Name}}}
}
