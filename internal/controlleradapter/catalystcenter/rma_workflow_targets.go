// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
	"time"
)

// Bind both replacement participants to the existing immutable group target
// contract. This is evidence collection only: no journal, reservation, Lease,
// adoption, node rename or native workflow is created by this observer.
func (a *adapter) observeOwnedReplacementTargets(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, ref configurationDeviceReference, want deviceReplacementIntent, now time.Time) ([]contract.GroupTargetIdentity, error) {
	if a == nil || config == nil {
		return nil, errConfigurationTransition
	}
	before, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	devices, err := a.observeOwnedReplacementReadiness(ctx, config, source, ref, want, now)
	if err != nil {
		return nil, err
	}
	if len(devices) != 2 {
		return nil, errConfigurationDeviceAdmission
	}
	bindings, err := resolveConfigurationManagedBindings(ctx, a.statusReader, before.Namespace, devices)
	if err != nil {
		return nil, err
	}
	targets, err := configurationGroupTargets(before.Namespace, bindings)
	if err != nil || len(targets) != 2 {
		return nil, errConfigurationDeviceAdmission
	}
	// Native and Kubernetes reads are not a transaction. Repeat both sides and
	// require the complete identities/resource versions to remain unchanged.
	observed, err := a.observeOwnedReplacementReadiness(ctx, config, source, ref, want, now)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(devices, observed) {
		return nil, errConfigurationStale
	}
	refreshed, err := resolveConfigurationManagedBindings(ctx, a.statusReader, before.Namespace, observed)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(bindings, refreshed) {
		return nil, errConfigurationStale
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	after, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if after.UID != before.UID || after.ResourceVersion != before.ResourceVersion {
		return nil, errConfigurationStale
	}
	return targets, nil
}
