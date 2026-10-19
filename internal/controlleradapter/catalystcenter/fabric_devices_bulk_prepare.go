// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func declaredFabricDevicesBulk(source *configSourceSnapshot, wants []fabricDeviceIntent) error {
	if source == nil || len(wants) == 0 || len(wants) > fabricDevicesBulkLimit {
		return errConfigurationTransition
	}
	declared, err := parseFabricDeviceIntents(source.raw)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	rootCohorts := map[string]bool{}
	for _, candidate := range declared {
		if candidate.fabricZone == "" {
			rootCohorts[candidate.fabricSite] = true
		}
	}
	for _, want := range wants {
		name := want.inventory.reference.Name
		if name == "" || seen[name] {
			return errConfigurationStale
		}
		seen[name] = true
		matches := 0
		for _, candidate := range declared {
			if candidate.inventory.reference.Name == name {
				// The pinned root cohort includes zoned declarations when the
				// same site also contains an unzoned declaration.
				if want.fabricZone == "" && candidate.fabricZone != "" && rootCohorts[candidate.fabricSite] {
					candidate.fabricZone = ""
				}
				if !reflect.DeepEqual(candidate, want) {
					return errConfigurationStale
				}
				matches++
			}
		}
		if matches != 1 {
			return errConfigurationStale
		}
	}
	return nil
}

// Record the complete physical group with the existing shared journal CAS.
// Preparation never invokes the native closure or creates grants/claims.
func (a *adapter) prepareFabricDevicesBulk(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, wants []fabricDeviceIntent) (*cvk.NetworkControllerConfigurationOperation, error) {
	return a.prepareFabricDevicesBulkObserved(ctx, config, source, wants, nil)
}

func (a *adapter) prepareFabricDevicesBulkObserved(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, wants []fabricDeviceIntent, preflight func(context.Context) error) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || config == nil || !a.configurationGroupAdmissionReady() || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 || config.Spec.PrunePolicy == configapi.NetworkControllerRetentionPolicyDelete {
		return nil, errConfigurationDeviceAdmission
	}
	if err := declaredFabricDevicesBulk(source, wants); err != nil {
		return nil, err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	write, targets, err := a.client.planManagedFabricDevicesBulk(ctx, a.statusReader, config.Namespace, wants, func(context.Context, []Device) error { return errConfigurationDeviceAdmission })
	if err != nil {
		return nil, err
	}
	if preflight != nil {
		if err := preflight(ctx); err != nil {
			return nil, err
		}
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if fresh.UID != nc.UID || fresh.ResourceVersion != nc.ResourceVersion {
		return nil, errConfigurationStale
	}
	if write == nil {
		return nil, nil
	}
	identity := write.identity
	identity.groupTargets = targets
	return a.prepareConfiguration(ctx, config, source, "fabric", write.resource, write.action, identity)
}
