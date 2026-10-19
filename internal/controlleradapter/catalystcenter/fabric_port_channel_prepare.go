// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func declaredFabricPortChannel(source *configSourceSnapshot, device string, id int64) (*fabricPortChannelDeploymentIntent, error) {
	if source == nil {
		return nil, errConfigurationTransition
	}
	model, err := parseFabricEdgeDeployment(source.raw)
	if err != nil {
		return nil, err
	}
	intents := model.channels
	var selected *fabricPortChannelDeploymentIntent
	for i := range intents {
		candidate := &intents[i]
		if candidate.device.inventory.reference.Name == device && *candidate.channel.ID == id {
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

func (c *client) planManagedFabricPortChannel(ctx context.Context, reader ctrlclient.Reader, namespace string, want fabricPortChannelDeploymentIntent) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	deps, targets, err := c.fabricPortChannelTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	// Preparation has no authority to submit. A separately permitted dispatch
	// must construct a guarded native write after the complete group is claimed.
	write, err := c.planGuardedFabricPortChannelCreate(ctx, want, func(context.Context) error { return errConfigurationDeviceAdmission })
	if err != nil {
		return nil, nil, err
	}
	if write != nil && (write.resource != fabricPortChannelResource(deps) || write.identity.target != deps.deviceID || write.identity.parent != deps.fabricID) {
		return nil, nil, errConfigurationStale
	}
	fresh, current, err := c.fabricPortChannelTargets(ctx, reader, namespace, want)
	if err != nil {
		return nil, nil, err
	}
	if fresh.digest != deps.digest || !reflect.DeepEqual(targets, current) {
		return nil, nil, errConfigurationStale
	}
	return write, targets, nil
}

func (a *adapter) prepareFabricPortChannel(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, device string, id int64) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 {
		return nil, errConfigurationTransition
	}
	want, err := declaredFabricPortChannel(source, device, id)
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
	write, targets, err := a.client.planManagedFabricPortChannel(ctx, a.statusReader, config.Namespace, *want)
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

func (a *adapter) revalidateFabricPortChannelOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, device string, id int64, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Action != "Create" || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) || op.TaskID != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero())) {
		return errConfigurationTransition
	}
	if err := contract.ValidateGroupJournalIdentity(op); err != nil {
		return errConfigurationStale
	}
	want, err := declaredFabricPortChannel(source, device, id)
	if err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		write, targets, err := a.client.planManagedFabricPortChannel(ctx, a.statusReader, config.Namespace, *want)
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
