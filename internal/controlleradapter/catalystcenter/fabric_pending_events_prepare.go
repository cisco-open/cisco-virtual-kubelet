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
func (a *adapter) preparePendingFabricEvent(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, want fabricSiteIntent, eventID string) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || config == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 || !validAPIID(eventID) {
		return nil, errConfigurationTransition
	}
	if err := declaredPendingFabricEvent(config, source, want); err != nil {
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
	snapshot, targets, err := a.client.pendingFabricEventTargets(ctx, a.statusReader, config.Namespace, want)
	if err != nil {
		return nil, err
	}
	var selected *fabricPendingEvent
	for _, event := range snapshot.events {
		if event.ID == eventID {
			copy := event
			selected = &copy
			break
		}
	}
	if selected == nil {
		return nil, errConfigurationStale
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
	return a.prepareConfiguration(ctx, config, source, "fabric", pendingFabricEventResource(want.path, snapshot.site.siteID, *selected), "Update", configurationResourceIdentity{target: selected.ID, parent: selected.FabricID, groupTargets: targets})
}
