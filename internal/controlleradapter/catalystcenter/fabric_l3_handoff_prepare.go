// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Prepare only an explicitly declared action with freshly resolved native and
// managed identities. This retains uncertainty in the shared controller journal;
// it does not create reservations or submit a native request.
func (a *adapter) prepareFabricL3IPHandoff(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, key fabricL3IPHandoffIntent) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 || source == nil {
		return nil, errConfigurationTransition
	}
	selected, err := declaredFabricL3IPHandoff(source, key)
	if err != nil {
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
	write, targets, err := a.client.planManagedFabricL3IPHandoff(ctx, a.statusReader, config.Namespace, *selected)
	if err != nil {
		return nil, err
	}
	if write == nil {
		return nil, nil
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
	identity := write.identity
	identity.groupTargets = targets
	return a.prepareConfiguration(ctx, config, source, "fabric", write.resource, write.action, identity)
}
