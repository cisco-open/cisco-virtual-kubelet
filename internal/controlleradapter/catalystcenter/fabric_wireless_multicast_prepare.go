// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	"reflect"
)

// Consume the complete fabric model and require prerequisite effects to already
// match. Wired multicast execution is separate and cannot be silently dropped.
func (a *adapter) declaredFabricWirelessMulticast(ctx context.Context, source *configSourceSnapshot, path string) (bool, error) {
	if source == nil {
		return false, errConfigurationTransition
	}
	plan, err := a.client.compileFabricConfiguration(source.raw)
	if err != nil {
		return false, err
	}
	executor := plan.configurationExecutor
	foundationReports := len(plan.reports) - 1
	if mappings, ok := executor.(*fabricVLANSSIDConfiguration); ok {
		drift, err := mappings.report(ctx)
		if err != nil {
			return false, err
		}
		if len(drift) != 0 {
			return false, errConfigurationBusy
		}
		executor = mappings.foundation
		foundationReports--
	}
	multicast, ok := executor.(*fabricMulticastConfiguration)
	if !ok {
		return false, errConfigurationTransition
	}
	found, enabled := false, false
	for _, site := range multicast.desired {
		if site.intent.replication != "" || len(site.intent.networks) != 0 {
			return false, errConfigurationDeviceAdmission
		}
		if site.path == path && site.intent.wireless != nil {
			if found {
				return false, errConfigurationStale
			}
			found, enabled = true, *site.intent.wireless
		}
	}
	if !found {
		return false, errConfigurationStale
	}
	// The compiler appends multicast reporting after all foundation reports.
	for _, report := range plan.reports[:foundationReports] {
		drift, err := report(ctx)
		if err != nil {
			return false, err
		}
		if len(drift) != 0 {
			return false, errConfigurationBusy
		}
	}
	return enabled, nil
}

func (a *adapter) prepareFabricWirelessMulticast(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 {
		return nil, errConfigurationTransition
	}
	want, err := a.declaredFabricWirelessMulticast(ctx, source, path)
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
	write, targets, err := a.client.planManagedFabricWirelessMulticast(ctx, a.statusReader, config.Namespace, path, want)
	if err != nil || write == nil {
		return nil, err
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

func (a *adapter) revalidateFabricWirelessMulticastOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Action != "Update" || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) || op.TaskID != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero())) {
		return errConfigurationTransition
	}
	if err := contract.ValidateGroupJournalIdentity(op); err != nil {
		return errConfigurationStale
	}
	want, err := a.declaredFabricWirelessMulticast(ctx, source, path)
	if err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		write, targets, err := a.client.planManagedFabricWirelessMulticast(ctx, a.statusReader, config.Namespace, path, want)
		if err != nil {
			return false, err
		}
		if write == nil || write.resource != op.Resource || write.action != op.Action || write.identity.target != op.TargetID || write.identity.parent != op.ParentID || !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return false, errConfigurationStale
		}
		return true, nil
	})
	if err != nil {
		return err
	}
	if !ok {
		return errConfigurationBusy
	}
	return nil
}
