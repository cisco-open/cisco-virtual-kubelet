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

func declaredFabricL2Handoff(source *configSourceSnapshot, key fabricL2HandoffIntent) (*fabricL2HandoffDeploymentIntent, error) {
	if source == nil {
		return nil, errConfigurationTransition
	}
	_, intents, _, _, err := parseAllFabricHandoffDeployment(source.raw)
	if err != nil {
		return nil, err
	}
	var selected *fabricL2HandoffDeploymentIntent
	for i := range intents {
		if intents[i].handoff == key {
			if selected != nil {
				return nil, errConfigurationStale
			}
			selected = &intents[i]
		}
	}
	if selected == nil {
		return nil, errConfigurationStale
	}
	return selected, nil
}

// Re-observe a stored operation around the shared group admission boundary.
// This is not a dispatch permit and never submits the newly constructed plan.
// Submitting is only for the caller holding the one-use group dispatch permit;
// it must never be used to replay a submission after restart.
func (a *adapter) revalidateFabricL2HandoffOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, key fabricL2HandoffIntent, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Action != "Create" || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) || op.TaskID != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero())) {
		return errConfigurationTransition
	}
	if err := contract.ValidateGroupJournalIdentity(op); err != nil {
		return errConfigurationStale
	}
	want, err := declaredFabricL2Handoff(source, key)
	if err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		write, targets, err := a.client.planManagedFabricL2Handoff(ctx, a.statusReader, config.Namespace, *want)
		if err != nil {
			return false, err
		}
		// An already-present mapping cannot become evidence that this unsubmitted
		// journal created it. Keep ownership and current compliance separate.
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
