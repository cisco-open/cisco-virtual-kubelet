// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	corev1 "k8s.io/api/core/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
	"sort"
)

type configurationManagedBinding struct {
	inventory Device
	device    *cvk.CiscoDevice
	node      *corev1.Node
}

// Resolve the complete footprint as one evidence set. Reuse the existing
// device/Node identity rules and list declarations across namespaces to detect
// aliases. This neither acquires reservations nor grants dispatch permission.
func resolveConfigurationManagedBindings(ctx context.Context, reader ctrlclient.Reader, namespace string, targets []Device) ([]configurationManagedBinding, error) {
	if reader == nil || namespace == "" || len(targets) > 1000 {
		return nil, errConfigurationDeviceAdmission
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, target := range targets {
		if seen[target.ID] {
			return nil, errInvalidResponse
		}
		seen[target.ID] = true
		ids = append(ids, target.ID)
	}
	canonical, err := bindMulticastRPDevices(ids, targets)
	if err != nil {
		return nil, err
	}
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Serial < canonical[j].Serial })
	if len(canonical) == 0 {
		return []configurationManagedBinding{}, nil
	}
	var declared cvk.CiscoDeviceList
	if err := reader.List(ctx, &declared); err != nil {
		return nil, err
	}
	out := []configurationManagedBinding{}
	nodeUIDs := map[string]bool{}
	for _, target := range canonical {
		device, err := configurationManagedDevice(namespace, target, declared.Items)
		if err != nil {
			return nil, err
		}
		var node corev1.Node
		if err := reader.Get(ctx, ctrlclient.ObjectKey{Name: device.Status.NodeIdentity.NodeName}, &node); err != nil {
			return nil, err
		}
		if err := configurationManagedNode(device, &node); err != nil {
			return nil, err
		}
		if nodeUIDs[string(node.UID)] {
			return nil, errConfigurationStale
		}
		nodeUIDs[string(node.UID)] = true
		out = append(out, configurationManagedBinding{target, device, node.DeepCopy()})
	}
	// Re-list once for the whole set to detect declaration changes and aliases
	// appearing while other targets were read, rather than N repeated full lists.
	var refreshed cvk.CiscoDeviceList
	if err := reader.List(ctx, &refreshed); err != nil {
		return nil, err
	}
	for _, binding := range out {
		device, err := configurationManagedDevice(namespace, binding.inventory, refreshed.Items)
		if err != nil {
			return nil, err
		}
		if device.UID != binding.device.UID || device.ResourceVersion != binding.device.ResourceVersion {
			return nil, errConfigurationStale
		}
		var node corev1.Node
		if err := reader.Get(ctx, ctrlclient.ObjectKeyFromObject(binding.node), &node); err != nil {
			return nil, err
		}
		if err := configurationManagedNode(device, &node); err != nil {
			return nil, err
		}
		if node.ResourceVersion != binding.node.ResourceVersion {
			return nil, errConfigurationStale
		}
	}
	return out, nil
}

// Convert freshly validated bindings into the immutable future group contract.
// Namespace and identity checks are repeated to reject stale/cross-scope callers.
func configurationGroupTargets(namespace string, bindings []configurationManagedBinding) ([]controllerconfiguration.GroupTargetIdentity, error) {
	targets := make([]controllerconfiguration.GroupTargetIdentity, 0, len(bindings))
	for _, binding := range bindings {
		if binding.device == nil || binding.node == nil {
			return nil, errConfigurationStale
		}
		if _, err := configurationManagedDevice(namespace, binding.inventory, []cvk.CiscoDevice{*binding.device}); err != nil {
			return nil, err
		}
		if err := configurationManagedNode(binding.device, binding.node); err != nil {
			return nil, err
		}
		targets = append(targets, controllerconfiguration.GroupTargetIdentity{
			InventoryID: binding.inventory.ID, PhysicalIdentity: binding.inventory.Serial,
			Device: ops.ConfigurationReservationReference{Name: binding.device.Name, UID: string(binding.device.UID)},
			Node:   ops.ConfigurationReservationReference{Name: binding.node.Name, UID: string(binding.node.UID)},
		})
	}
	return controllerconfiguration.CanonicalGroupTargets(targets)
}
