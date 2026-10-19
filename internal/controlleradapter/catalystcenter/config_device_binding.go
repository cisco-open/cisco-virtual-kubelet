// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/topologyidentity"
)

// Resolve the managed Kubernetes identity before requesting a device mutation
// lease. A controller inventory record is not authority to adopt or rename a
// CiscoDevice. The caller must supply authoritative inventory of CiscoDevices;
// this helper grants neither lease ownership nor permission to mutate.
func configurationManagedDevice(namespace string, target Device, devices []cvk.CiscoDevice) (*cvk.CiscoDevice, error) {
	serial, err := topologyidentity.CanonicalPhysicalIdentity(target.Serial)
	if err != nil || namespace == "" || !validAPIID(target.ID) {
		return nil, errors.New("configuration target identity is incomplete")
	}
	var found *cvk.CiscoDevice
	for i := range devices {
		d := &devices[i]
		declared, err := topologyidentity.CanonicalPhysicalIdentity(d.Spec.PhysicalIdentity)
		if err != nil || declared != serial {
			continue
		}
		if found != nil {
			return nil, errors.New("physical device has multiple Kubernetes identities")
		}
		found = d
	}
	if found == nil {
		return nil, errors.New("physical device is not declared in Kubernetes")
	}
	d := found
	n := d.Status.NodeIdentity
	if d.Namespace != namespace || d.Name == "" || d.UID == "" || !d.DeletionTimestamp.IsZero() || d.Spec.Driver != cvk.DeviceDriverXE ||
		!sameManagementIP(d.Spec.Address, target.ManagementIP) || n == nil || n.DeviceUID != string(d.UID) || n.NodeUID == "" || n.NodeName == "" || n.PhysicalIdentity != serial {
		return nil, errors.New("configuration target does not match the managed device identity")
	}
	name := d.Spec.NodeName
	if name == "" {
		name = d.Name
	}
	if n.NodeName != name {
		return nil, errors.New("managed node name does not match the device declaration")
	}
	return d.DeepCopy(), nil
}

// Confirm the live Node incarnation and authenticated inventory independently
// of the CiscoDevice status projection. This remains evidence, not a grant.
func configurationManagedNode(device *cvk.CiscoDevice, node *corev1.Node) error {
	if device == nil || node == nil || device.Status.NodeIdentity == nil {
		return errors.New("managed node binding is unavailable")
	}
	identity := device.Status.NodeIdentity
	if device.UID == "" || identity.DeviceUID != string(device.UID) || node.UID == "" || string(node.UID) != identity.NodeUID || node.Name != identity.NodeName || !node.DeletionTimestamp.IsZero() {
		return errors.New("live node incarnation differs from managed device binding")
	}
	observed, err := topologyidentity.ObservedPhysicalIdentity(device.Spec.PhysicalIdentity, node.Status.NodeInfo.MachineID, node.Status.NodeInfo.SystemUUID)
	if err != nil || observed != identity.PhysicalIdentity {
		return errors.New("live node physical inventory differs from declared device identity")
	}
	return nil
}

// resolveConfigurationManagedBinding requires an uncached API reader. It
// collects identity evidence only; no mutation lease or submission grant is
// acquired here. Listing across namespaces is necessary to detect aliases of
// the same physical device rather than authorizing one ambiguous declaration.
func resolveConfigurationManagedBinding(ctx context.Context, reader ctrlclient.Reader, namespace string, target Device) (*cvk.CiscoDevice, *corev1.Node, error) {
	if reader == nil {
		return nil, nil, errors.New("configuration API reader is unavailable")
	}
	var declared cvk.CiscoDeviceList
	if err := reader.List(ctx, &declared); err != nil {
		return nil, nil, err
	}
	device, err := configurationManagedDevice(namespace, target, declared.Items)
	if err != nil {
		return nil, nil, err
	}
	var node corev1.Node
	if err := reader.Get(ctx, ctrlclient.ObjectKey{Name: device.Status.NodeIdentity.NodeName}, &node); err != nil {
		return nil, nil, err
	}
	if err := configurationManagedNode(device, &node); err != nil {
		return nil, nil, err
	}
	// Do not publish evidence assembled across a changed device declaration.
	var fresh cvk.CiscoDevice
	if err := reader.Get(ctx, ctrlclient.ObjectKeyFromObject(device), &fresh); err != nil {
		return nil, nil, err
	}
	if fresh.UID != device.UID || fresh.ResourceVersion != device.ResourceVersion {
		return nil, nil, errConfigurationStale
	}
	return device, node.DeepCopy(), nil
}
