// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"time"
)

// Prepare at most one fabric action per reconcile. Existing private planners own
// dependency/readback checks; only the shared journal may persist group scope.
// This does not create claims or invoke a native submission closure.
func (r *configReconciler) prepareConfigurationFabricGroup(ctx context.Context, nc *cvk.NetworkController, config *configapi.NetworkControllerConfig, source *configSourceSnapshot) (handled, converged bool, err error) {
	if len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "inventory" {
		return r.prepareConfigurationEWLCGroup(ctx, nc, config, source)
	}
	if !r.a.configurationGroupAdmissionReady() || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 {
		return false, false, nil
	}
	if handled, converged, err := r.prepareConfigurationExtranetGroup(ctx, nc, config, source); handled {
		return handled, converged, err
	}
	if handled, converged, err := r.prepareFabricAuthenticationGroup(ctx, nc, config, source); handled {
		return handled, converged, err
	}
	devices, l2, ip, sda, parseErr := parseAllFabricHandoffDeployment(source.raw)
	var channels []fabricPortChannelDeploymentIntent
	var assignments []fabricPortAssignmentDeploymentIntent
	var wireless []fabricMulticastSiteIntent
	var mappings []fabricVLANSSIDIntent
	if parseErr != nil {
		var model *fabricEdgeDeployment
		model, parseErr = parseFabricEdgeDeployment(source.raw)
		if parseErr == nil {
			devices, channels, assignments = model.devices, model.channels, model.assignments
		}
	}
	if parseErr != nil {
		plan, err := r.a.client.compileFabricConfiguration(source.raw)
		if err == nil {
			executor := plan.configurationExecutor
			if mapped, ok := executor.(*fabricVLANSSIDConfiguration); ok {
				mappings = mapped.desired
				executor = mapped.foundation
				parseErr = nil
			}
			if multicast, ok := executor.(*fabricMulticastConfiguration); ok {
				wireless = multicast.desired
				parseErr = nil
			}
		}
	}
	if parseErr != nil || len(l2)+len(ip)+len(sda)+len(channels)+len(assignments)+len(wireless)+len(mappings) == 0 {
		return false, false, nil
	}
	if nc.Status.Configuration != nil && nc.Status.Configuration.Operation != nil {
		return true, false, errConfigurationBusy
	}
	prepareCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	// Base fabric/device effects must already match. Do not drop declarations
	// merely because only their group action work is currently executable here.
	for _, device := range devices {
		deps, err := r.a.client.fabricDeviceDependencies(prepareCtx, device)
		if err != nil {
			return true, false, err
		}
		actual, exists := deps.before[deps.target.ID]
		if !exists || !fabricDeviceConfigurationMatches(deps.native, actual) {
			return true, false, errConfigurationBusy
		}
	}
	for _, site := range wireless {
		if site.intent.wireless != nil && (site.intent.replication != "" || len(site.intent.networks) > 0) || site.intent.replication == "" && len(site.intent.networks) == 0 && site.intent.wireless == nil {
			return true, false, errConfigurationDeviceAdmission
		}
		if site.intent.replication != "" {
			if _, err := r.a.declaredWiredMulticastFoundation(prepareCtx, source); err != nil {
				return true, false, err
			}
			if _, _, err := r.a.client.replicationTargets(prepareCtx, r.a.statusReader, config.Namespace, site.path); err != nil {
				return true, false, err
			}
		}
		if len(site.intent.networks) > 0 {
			if _, err := r.a.declaredFabricMulticastVN(prepareCtx, source, site.path, site.intent.networks[0].name); err != nil {
				return true, false, err
			}
			continue
		}
		if site.intent.replication != "" {
			continue
		}
		if len(mappings) > 0 {
			continue
		}
		if _, err := r.a.declaredFabricWirelessMulticast(prepareCtx, source, site.path); err != nil {
			return true, false, err
		}
	}
	for _, site := range mappings {
		if _, err := r.a.declaredFabricVLANSSIDs(prepareCtx, source, site.path); err != nil {
			return true, false, err
		}
	}
	if err := r.a.claimConfigurationSection(prepareCtx, config, source); err != nil {
		return true, false, err
	}
	observed, err := r.a.currentController(prepareCtx)
	if err != nil {
		return true, false, err
	}
	if observed.Status.Configuration != nil && observed.Status.Configuration.Operation != nil {
		return true, false, errConfigurationBusy
	}
	// Preflight every declaration before retaining the first pending action.
	// A later invalid component must not produce a partially applied manifest.
	var next *configurationWrite
	selectNext := func(write *configurationWrite, targets []contract.GroupTargetIdentity, err error) error {
		if err != nil {
			return err
		}
		if next == nil && write != nil {
			copy := *write
			copy.identity.groupTargets = targets
			next = &copy
		}
		return nil
	}
	for _, intent := range l2 {
		if err := selectNext(r.a.client.planManagedFabricL2Handoff(prepareCtx, r.a.statusReader, config.Namespace, intent)); err != nil {
			return true, false, err
		}
	}
	for _, intent := range ip {
		if err := selectNext(r.a.client.planManagedFabricL3IPHandoff(prepareCtx, r.a.statusReader, config.Namespace, intent)); err != nil {
			return true, false, err
		}
	}
	for _, intent := range sda {
		if err := selectNext(r.a.client.planManagedFabricSDAHandoff(prepareCtx, r.a.statusReader, config.Namespace, intent)); err != nil {
			return true, false, err
		}
	}
	for _, intent := range channels {
		if err := selectNext(r.a.client.planManagedFabricPortChannel(prepareCtx, r.a.statusReader, config.Namespace, intent)); err != nil {
			return true, false, err
		}
	}
	for _, intent := range assignments {
		deps, err := r.a.client.fabricPortAssignmentDependencies(prepareCtx, intent)
		if err != nil {
			return true, false, err
		}
		port := *intent.assignments[0].Interface
		if len(deps.selection.missing) > 0 {
			port = *deps.selection.missing[0].Interface
		}
		// The planner preflights every assignment in this device model, even though
		// only one missing port is journaled at a time.
		if err := selectNext(r.a.client.planManagedFabricPortAssignment(prepareCtx, r.a.statusReader, config.Namespace, intent, port)); err != nil {
			return true, false, err
		}
	}
	pendingMappings := map[string]bool{}
	for _, site := range mappings {
		owned, err := r.a.currentFabricVLANSSIDOwnership(prepareCtx, config, site.path)
		if err != nil {
			return true, false, err
		}
		write, targets, err := r.a.client.planManagedFabricVLANSSIDs(prepareCtx, r.a.statusReader, config.Namespace, site.path, site.mappings, owned)
		if err := selectNext(write, targets, err); err != nil {
			return true, false, err
		}
		pendingMappings[site.path] = write != nil
	}
	pendingVNs := false
	for _, site := range wireless {
		for _, want := range site.intent.networks {
			write, targets, err := r.a.client.planManagedFabricMulticastVN(prepareCtx, r.a.statusReader, config.Namespace, site.path, site.intent.networks, want.name)
			if err := selectNext(write, targets, err); err != nil {
				return true, false, err
			}
			pendingVNs = pendingVNs || write != nil
		}
	}
	for _, site := range wireless {
		if site.intent.replication != "" {
			if pendingVNs {
				continue
			}
			if _, err := r.a.declaredFabricReplication(prepareCtx, source, site.path); err != nil {
				return true, false, err
			}
			if err := selectNext(r.a.client.planManagedFabricReplication(prepareCtx, r.a.statusReader, config.Namespace, site.path, site.intent.replication)); err != nil {
				return true, false, err
			}
			continue
		}
		if site.intent.wireless == nil || pendingMappings[site.path] {
			continue
		}
		if err := selectNext(r.a.client.planManagedFabricWirelessMulticast(prepareCtx, r.a.statusReader, config.Namespace, site.path, *site.intent.wireless)); err != nil {
			return true, false, err
		}
	}

	if err := r.a.checkConfigurationSnapshot(prepareCtx, config, source); err != nil {
		return true, false, err
	}
	fresh, err := r.a.currentController(prepareCtx)
	if err != nil {
		return true, false, err
	}
	if fresh.UID != observed.UID || fresh.ResourceVersion != observed.ResourceVersion {
		return true, false, errConfigurationStale
	}
	if next != nil {
		_, err := r.a.prepareConfiguration(prepareCtx, config, source, "fabric", next.resource, next.action, next.identity)
		return true, false, err
	}
	return true, true, nil
}
