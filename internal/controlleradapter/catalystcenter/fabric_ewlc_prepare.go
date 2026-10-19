// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func declaredFabricEWLC(source *configSourceSnapshot, device string) (*fabricEWLCIntent, error) {
	if source == nil {
		return nil, errConfigurationTransition
	}
	model, err := parseFabricEWLCIntents(source.raw)
	if err != nil {
		return nil, err
	}
	intents := model
	var selected *fabricEWLCIntent
	for i := range intents {
		candidate := &intents[i]
		if candidate.device.inventory.reference.Name == device {
			if selected != nil {
				return nil, errConfigurationStale
			}
			selected = candidate
		}
	}
	if selected == nil {
		return nil, errConfigurationStale
	}
	return selected, nil
}

func (a *adapter) prepareFabricEWLC(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, device string) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 {
		return nil, errConfigurationTransition
	}
	want, err := declaredFabricEWLC(source, device)
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
	write, targets, err := a.client.planManagedFabricEWLC(ctx, a.statusReader, config.Namespace, *want)
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
	return a.prepareConfiguration(ctx, config, source, "inventory", write.resource, write.action, identity)
}

func (a *adapter) revalidateFabricEWLCOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, device string, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Action != "Update" || (op.Section != "inventory" || !validConfigurationGroupAction(op.Section, op.Resource, op.Action)) || op.TaskID != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero())) {
		return errConfigurationTransition
	}
	if err := contract.ValidateGroupJournalIdentity(op); err != nil {
		return errConfigurationStale
	}
	want, err := declaredFabricEWLC(source, device)
	if err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "inventory", func(ctx context.Context) (bool, error) {
		write, targets, err := a.client.planManagedFabricEWLC(ctx, a.statusReader, config.Namespace, *want)
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
