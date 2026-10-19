// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Derive observation scope from the actual compiled settings executor, including
// credential/settings composition. A resource spelling alone is insufficient.
// Each retained task observes only its target; unrelated pending declarations
// cannot enlarge its physical authority or strand acknowledged work.
func networkSettingsExecutor(executor configurationExecutor) *networkConfigurationExecutor {
	var settings *networkConfigurationExecutor
	switch e := executor.(type) {
	case *networkConfigurationExecutor:
		settings = e
	case *networkSectionExecutor:
		settings = e.settings
	case *networkCredentialConfigurationExecutor:
		if e.network != nil {
			settings = e.network.settings
		}
	case *aaaNetworkConfigurationExecutor:
		settings = networkSettingsExecutor(e.settings)
	}
	return settings
}

func networkSettingTaskSelections(executor configurationExecutor) map[string]*networkConfigurationExecutor {
	settings := networkSettingsExecutor(executor)
	if settings == nil {
		return nil
	}
	frozen := freezeNetworkExecutor(settings)
	selections := map[string]*networkConfigurationExecutor{}
	for _, binding := range frozen.bindings {
		for _, setting := range binding.settings {
			if _, err := networkSettingPayload(setting); err != nil {
				return nil
			}
			resource := networkSettingLogicalKey(networkSettingIdentity(binding.path, setting.kind))
			if _, exists := selections[resource]; exists {
				return nil
			}
			selections[resource] = &networkConfigurationExecutor{client: frozen.client, comparison: frozen.comparison, bindings: []networkBinding{{path: binding.path, kind: binding.kind, settings: []networkSetting{setting}}}}
		}
	}
	return selections
}

// Existing metadata envelope, with a recognized site-setting Update identity.
// This is read permission only: no taskless recovery, replay, creator ownership,
// physical admission or credential/IPAM recovery is introduced.
func networkSettingKnownTaskSupported(op *cvk.NetworkControllerConfigurationOperation) bool {
	return definitionUpdateKnownTaskRecoverySupported(op, "network_settings") && networkSettingNoDispatchOperation(op)
}

func (a *adapter) bindNetworkSettingTaskRecovery(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, plan *configurationApplyPlan) {
	if a == nil || a.client == nil || config == nil || source == nil || plan == nil || plan.section != "network_settings" {
		return
	}
	selections := networkSettingTaskSelections(plan.executor)
	if len(selections) == 0 {
		return
	}
	previousRecovery, previousCompletion := plan.recoverKnownTask, plan.observeCompletion
	selected := func(op *cvk.NetworkControllerConfigurationOperation) bool {
		if !networkSettingKnownTaskSupported(op) {
			return false
		}
		key, ok := networkSettingResourceKey(op.Resource)
		return ok && selections[key] != nil
	}
	plan.recoverKnownTask = func(op *cvk.NetworkControllerConfigurationOperation) bool {
		return selected(op) || previousRecovery != nil && previousRecovery(op)
	}
	frozenConfig := config.DeepCopy()
	plan.observeCompletion = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
		if selected(op) {
			return a.observeOwnedConfigurationOperation(ctx, frozenConfig, source, op, "network_settings", func(ctx context.Context) (bool, error) {
				key, _ := networkSettingResourceKey(op.Resource)
				return selections[key].observeRetainedTask(ctx, op, now)
			})
		}
		// Preserve normal Accepted completion and all other existing callbacks.
		if previousCompletion != nil {
			return previousCompletion(ctx, op, now)
		}
		if op == nil || op.SubmittedAt == nil {
			return false, errConfigurationTransition
		}
		return a.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	}
}

func (e *networkConfigurationExecutor) observeRetainedTask(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if e == nil || e.client == nil || !networkSettingKnownTaskSupported(op) || len(e.bindings) != 1 || len(e.bindings[0].settings) != 1 || now.IsZero() {
		return false, errConfigurationTransition
	}
	binding := e.bindings[0]
	want := binding.settings[0]
	logical := networkSettingIdentity(binding.path, want.kind)
	if !networkSettingResourceMatches(op.Resource, logical) {
		return false, errConfigurationStale
	}
	before, err := e.observeSubmission(ctx)
	if err != nil {
		return false, err
	}
	if before.sites[binding.path].id != op.TargetID {
		return false, errConfigurationStale
	}
	done, err := e.client.configurationTaskComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		return false, err
	}
	done, err = e.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	if err != nil || !done {
		return false, err
	}
	after, err := e.observeSubmission(ctx)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(before, after) {
		return false, errConfigurationStale
	}
	if _, private := networkPrivateParts(op.Resource); private {
		return networkMatchPrivate(ctx, e.comparison, op.Resource, binding.path, after)
	}
	if want.kind == "telemetry" && e.comparison != nil {
		return false, errNetworkNativeBaseline
	}
	return len(networkSettingDrift(binding.path, want, after.effective[logical])) == 0, nil
}
