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
func (a *adapter) declaredFabricReplication(ctx context.Context, source *configSourceSnapshot, path string) (string, error) {
	multicast, err := a.declaredWiredMulticastFoundation(ctx, source)
	if err != nil {
		return "", err
	}
	mode := ""
	for _, site := range multicast.desired {
		if site.path == path && site.intent.replication != "" {
			if mode != "" {
				return "", errConfigurationStale
			}
			mode = site.intent.replication
		}
		if len(site.intent.networks) > 0 {
			snapshot, err := a.client.multicastSnapshot(ctx, site.path, site.intent.networks)
			if err != nil {
				return "", err
			}
			for _, want := range site.intent.networks {
				observed, exists := snapshot.before[want.name]
				if !exists {
					return "", errConfigurationBusy
				}
				same, err := fabricMulticastMatches(want, snapshot.fabricID, snapshot.nameIDs, observed)
				if err != nil {
					return "", err
				}
				if !same {
					return "", errConfigurationBusy
				}
			}
		}
	}
	if mode == "" {
		return "", errConfigurationStale
	}

	return mode, nil
}

func (a *adapter) prepareFabricReplication(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 {
		return nil, errConfigurationTransition
	}
	want, err := a.declaredFabricReplication(ctx, source, path)
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
	write, targets, err := a.client.planManagedFabricReplication(ctx, a.statusReader, config.Namespace, path, want)
	if err != nil {
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
	if write == nil {
		return nil, nil
	}
	identity := write.identity
	identity.groupTargets = targets
	return a.prepareConfiguration(ctx, config, source, "fabric", write.resource, write.action, identity)
}

func (a *adapter) revalidateFabricReplicationOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Action != "Update" || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) || op.TaskID != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero())) {
		return errConfigurationTransition
	}
	if err := contract.ValidateGroupJournalIdentity(op); err != nil {
		return errConfigurationStale
	}
	want, err := a.declaredFabricReplication(ctx, source, path)
	if err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		write, targets, err := a.client.planManagedFabricReplication(ctx, a.statusReader, config.Namespace, path, want)
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
