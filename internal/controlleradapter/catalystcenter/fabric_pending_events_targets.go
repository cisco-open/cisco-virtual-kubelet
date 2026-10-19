// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Collect native and Kubernetes identity as one repeated evidence set. This
// grants no dispatch permit: ownership and group reservations remain separate.
func (c *client) pendingFabricEventTargets(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricSiteIntent) (*fabricPendingEventsSnapshot, []contract.GroupTargetIdentity, error) {
	if reader == nil || namespace == "" {
		return nil, nil, errConfigurationDeviceAdmission
	}
	snapshot, err := c.pendingFabricEventsSnapshot(ctx, want)
	if err != nil {
		return nil, nil, err
	}
	bindings, err := resolveConfigurationManagedBindings(ctx, reader, namespace, snapshot.devices)
	if err != nil {
		return nil, nil, err
	}
	targets, err := configurationGroupTargets(namespace, bindings)
	if err != nil {
		return nil, nil, err
	}
	if err := c.revalidatePendingFabricEvents(ctx, want, snapshot); err != nil {
		return nil, nil, err
	}
	refreshed, err := resolveConfigurationManagedBindings(ctx, reader, namespace, snapshot.devices)
	if err != nil {
		return nil, nil, err
	}
	if !reflect.DeepEqual(bindings, refreshed) {
		return nil, nil, errConfigurationStale
	}
	return snapshot, targets, nil
}
