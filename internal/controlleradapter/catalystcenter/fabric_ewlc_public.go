// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

var errFabricEWLCDisableUnavailable = errors.New("embedded-WLC disable reload verification is unavailable")

func (r *configReconciler) prepareConfigurationEWLCGroup(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (bool, bool, error) {
	if !r.a.configurationGroupAdmissionReady() || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 || source == nil {
		return false, false, nil
	}
	intents, err := parseFabricEWLCIntents(source.raw)
	if err != nil || len(intents) == 0 {
		return false, false, nil
	}
	if config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return true, false, errConfigurationDeviceAdmission
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return true, false, errConfigurationBusy
	}
	for _, want := range intents {
		if !want.settings.enabled {
			return true, false, errFabricEWLCDisableUnavailable
		}
	}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	if err := r.a.claimConfigurationSection(ctx, config, source); err != nil {
		return true, false, err
	}
	observed, err := r.a.currentController(ctx)
	if err != nil {
		return true, false, err
	}
	if observed.Status.Configuration != nil && observed.Status.Configuration.Operation != nil {
		return true, false, errConfigurationBusy
	}
	devices, err := r.a.client.configurationDevices(ctx)
	if err != nil {
		return true, false, err
	}
	seen := map[string]bool{}
	var next *configurationWrite
	for _, want := range intents {
		device, err := resolveConfigurationDevice(want.device.inventory.reference, devices)
		if err != nil {
			return true, false, err
		}
		if seen[device.ID] {
			return true, false, errConfigurationStale
		}
		seen[device.ID] = true
		write, targets, err := r.a.client.planManagedFabricEWLC(ctx, r.a.statusReader, config.Namespace, want)
		if err != nil {
			return true, false, err
		}
		if next == nil && write != nil {
			copy := *write
			copy.identity.groupTargets = targets
			next = &copy
		}
	}
	if err := r.a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return true, false, err
	}
	fresh, err := r.a.currentController(ctx)
	if err != nil {
		return true, false, err
	}
	if fresh.UID != observed.UID || fresh.ResourceVersion != observed.ResourceVersion {
		return true, false, errConfigurationStale
	}
	if next == nil {
		return true, true, nil
	}
	_, err = r.a.prepareConfiguration(ctx, config, source, "inventory", next.resource, next.action, next.identity)
	return true, false, err
}
