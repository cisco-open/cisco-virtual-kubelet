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

// Keep every foundation effect in the same source visible. This stage may
// update an existing selected profile only after the declared foundation has
// converged; it cannot silently provision a fabric or discard another section.
func (a *adapter) declaredFabricAuthentication(ctx context.Context, source *configSourceSnapshot, path string) (fabricAuthIntent, error) {
	if a == nil || a.client == nil || source == nil {
		return fabricAuthIntent{}, errConfigurationTransition
	}
	plan, err := a.client.compileFabricConfiguration(source.raw)
	if err != nil {
		return fabricAuthIntent{}, err
	}
	auth, ok := plan.configurationExecutor.(*fabricAuthConfiguration)
	if !ok || len(plan.reports) < 2 {
		return fabricAuthIntent{}, errConfigurationTransition
	}
	var selected *fabricAuthIntent
	for _, w := range auth.desired {
		if w.site.path == path {
			if selected != nil {
				return fabricAuthIntent{}, errConfigurationStale
			}
			copy := w
			selected = &copy
		}
	}
	if selected == nil {
		return fabricAuthIntent{}, errConfigurationStale
	}
	for _, report := range plan.reports[:len(plan.reports)-1] {
		drift, err := report(ctx)
		if err != nil {
			return fabricAuthIntent{}, err
		}
		if len(drift) != 0 {
			return fabricAuthIntent{}, errConfigurationBusy
		}
	}
	// Validate all selected profiles, including later overrides, without requiring
	// them to converge before the first independently journaled update.
	if _, err := auth.report(ctx); err != nil {
		return fabricAuthIntent{}, err
	}
	return *selected, nil
}

func (a *adapter) prepareFabricAuthentication(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 {
		return nil, errConfigurationTransition
	}
	want, err := a.declaredFabricAuthentication(ctx, source, path)
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
	if nc.Status.Configuration != nil {
		if nc.Status.Configuration.Operation != nil {
			return nil, errConfigurationBusy
		}
		for _, owner := range nc.Status.Configuration.Owners {
			if owner.Section == "fabric" && (owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name) {
				return nil, errConfigurationOwned
			}
		}
	}
	write, targets, err := a.client.planManagedFabricAuthentication(ctx, a.statusReader, config.Namespace, want)
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

func (a *adapter) revalidateFabricAuthenticationOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Section != "fabric" || op.Action != "Update" || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) || op.TaskID != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || phase == "Prepared" && op.SubmittedAt != nil || phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero()) {
		return errConfigurationTransition
	}
	if contract.ValidateGroupJournalIdentity(op) != nil {
		return errConfigurationStale
	}
	want, err := a.declaredFabricAuthentication(ctx, source, path)
	if err != nil {
		return err
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		write, targets, err := a.client.planManagedFabricAuthentication(ctx, a.statusReader, config.Namespace, want)
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
