// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// This private bridge is for an admitted manager with an uncached reader. The
// supplied authorization must independently validate current source ownership,
// authenticated destination identity and rollout protection. A cordon alone
// is not that authority. Neither this helper nor the controller worker acquires
// authority, settles maintenance, uncordons a Node or declares convergence.
type managementAddressRolloutAuthorization func(context.Context, *cvk.NetworkController, *ops.ControllerConfigurationReservation, *cvk.CiscoDevice, *corev1.Node) error

func applyManagementAddressDeclaration(ctx context.Context, reader ctrlclient.Reader, writer ctrlclient.Client, expected *ops.ControllerConfigurationReservation, ref configurationDeviceReference, now time.Time, authorize managementAddressRolloutAuthorization) error {
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || authorize == nil || now.IsZero() {
		return errConfigurationDeviceAdmission
	}
	read := func() (*cvk.NetworkController, *ops.ControllerConfigurationReservation, *cvk.CiscoDevice, *corev1.Node, *cvk.CiscoDevice, error) {
		var r ops.ControllerConfigurationReservation
		if err := reader.Get(ctx, ctrlclient.ObjectKeyFromObject(expected), &r); err != nil {
			return nil, nil, nil, nil, nil, err
		}
		if r.UID != expected.UID || r.ResourceVersion != expected.ResourceVersion || !reflect.DeepEqual(r.Spec, expected.Spec) || !r.DeletionTimestamp.IsZero() {
			return nil, nil, nil, nil, nil, errConfigurationStale
		}
		var nc cvk.NetworkController
		if err := reader.Get(ctx, ctrlclient.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}, &nc); err != nil {
			return nil, nil, nil, nil, nil, err
		}
		var device cvk.CiscoDevice
		var node corev1.Node
		if err := reader.Get(ctx, ctrlclient.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Device.Name}, &device); err != nil {
			return nil, nil, nil, nil, nil, err
		}
		if err := reader.Get(ctx, ctrlclient.ObjectKey{Name: r.Spec.Node.Name}, &node); err != nil {
			return nil, nil, nil, nil, nil, err
		}
		if !node.Spec.Unschedulable {
			return nil, nil, nil, nil, nil, errConfigurationBusy
		}
		proposed, err := planManagementAddressDeclaration(ref, &nc, &r, &device, &node, now)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		// Reuse the global alias and exact old-endpoint identity checks. Do not
		// replace current native inventory with a fabricated old address.
		target := Device{ID: r.Spec.InventoryID, Serial: r.Spec.PhysicalIdentity, ManagementIP: r.Spec.ManagementAddress}
		boundDevice, boundNode, err := resolveConfigurationManagedBinding(ctx, reader, r.Namespace, target)
		if err != nil {
			return nil, nil, nil, nil, nil, err
		}
		if !reflect.DeepEqual(boundDevice, &device) || !reflect.DeepEqual(boundNode, &node) {
			return nil, nil, nil, nil, nil, errConfigurationStale
		}
		return &nc, &r, &device, &node, proposed, nil
	}
	nc, r, device, node, _, err := read()
	if err != nil {
		return err
	}
	if err := authorize(ctx, nc.DeepCopy(), r.DeepCopy(), device.DeepCopy(), node.DeepCopy()); err != nil {
		return err
	}
	freshNC, freshR, freshDevice, freshNode, proposed, err := read()
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(nc, freshNC) || !reflect.DeepEqual(r, freshR) || !reflect.DeepEqual(device, freshDevice) || !reflect.DeepEqual(node, freshNode) {
		return errConfigurationStale
	}
	// Only the address is patched, with a resourceVersion precondition. Return
	// conflicts to the manager for fresh authorization; never blindly retry or
	// replay the native task. Cross-object reads are not an API transaction: the
	// manager's admission policy must enforce continued rollout protection.
	return writer.Patch(ctx, proposed, ctrlclient.MergeFromWithOptions(freshDevice, ctrlclient.MergeFromWithOptimisticLock{}))
}
