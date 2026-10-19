// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Keep the existing operation/receipt format: create has no native policy UUID
// yet, whereas an expansion binds the existing UUID. Neither invents a parent.
func extranetOperationTarget(resource, action string) (string, error) {
	if action == "Create" && validExtranetCreateResource(resource) {
		return "", nil
	}
	if action == "Update" {
		suffix, ok := strings.CutPrefix(resource, "extranet-update-v2:")
		parts := strings.Split(suffix, ":")
		if ok && len(parts) == 3 && validAPIID(parts[0]) && validExtranetCreateResource("extranet-create:"+parts[1]) && validExtranetCreateResource("extranet-create:"+parts[2]) {
			return parts[0], nil
		}
	}
	return "", errConfigurationTransition
}

// Native v1 verification remains available for retained receipts. A v2 prepared
// update additionally pins its old-policy digest for exact resume; verification
// compares its independent result/dependency digest after the policy changes.
func extranetUpdateNativeResource(resource string) (string, error) {
	if strings.HasPrefix(resource, "extranet-update-v2:") {
		target, err := extranetOperationTarget(resource, "Update")
		if err != nil {
			return "", err
		}
		parts := strings.Split(strings.TrimPrefix(resource, "extranet-update-v2:"), ":")
		return "extranet-update:" + target + ":" + parts[2], nil
	}
	suffix, ok := strings.CutPrefix(resource, "extranet-update:")
	parts := strings.Split(suffix, ":")
	if !ok || len(parts) != 2 || !validAPIID(parts[0]) || !validExtranetCreateResource("extranet-create:"+parts[1]) {
		return "", errConfigurationTransition
	}
	return resource, nil
}

// Consume the complete fabric source. Existing metadata prerequisites must
// converge before policy preparation; no prerequisite write is dispatched here.
// Unknown collections or still-gated compositions remain errors.
func (a *adapter) declaredExtranetFoundation(ctx context.Context, source *configSourceSnapshot) ([]fabricExtranetIntent, error) {
	if source == nil {
		return nil, errConfigurationStale
	}
	if _, err := a.client.compileFabricConfiguration(source.raw); err != nil {
		return nil, err
	}
	node, err := nacmodel.ParseDocument(source.raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil || len(root) != 1 {
		return nil, errInvalidResponse
	}
	fabric, ok := root["fabric"].(map[string]any)
	if !ok {
		return nil, errInvalidResponse
	}
	wants, err := parseFabricExtranets(fabric["extranet_policies"])
	if err != nil {
		return nil, err
	}
	delete(fabric, "extranet_policies")
	if len(fabric) != 0 {
		projection, err := json.Marshal(map[string]any{"catalyst_center": root})
		if err != nil {
			return nil, err
		}
		foundation, err := a.client.compileFabricConfiguration(projection)
		if err != nil {
			return nil, err
		}
		write, err := foundation.next(ctx)
		if err != nil {
			return nil, err
		}
		if write != nil {
			return nil, errConfigurationBusy
		}
	}
	return wants, nil
}

func (a *adapter) planDeclaredExtranet(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string) (*configurationWrite, []contract.GroupTargetIdentity, error) {
	wants, err := a.declaredExtranetFoundation(ctx, source)
	if err != nil {
		return nil, nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, nil, err
	}
	// An empty selection is used only by public whole-source reconciliation to
	// choose the first pending policy after validating every declaration.
	found := name == ""
	var selected *configurationWrite
	var targets []contract.GroupTargetIdentity
	// Validate every declared policy before returning the selected write. A later
	// unowned expansion or invalid scope must not allow an earlier submission.
	for _, want := range wants {
		state, err := a.client.extranetSnapshot(ctx, want)
		if err != nil {
			return nil, nil, err
		}
		// A retained creation receipt pins the original native incarnation even
		// when fields already match. Missing/recreated policies need a qualified
		// retirement/reapplication path, not an implicit new create or adoption.
		prefix, err := extranetReceiptPrefix(want.policy.Name)
		if err != nil {
			return nil, nil, err
		}
		retained := false
		if nc.Status.Configuration != nil {
			for _, receipt := range nc.Status.Configuration.ActionApplications {
				retained = retained || receipt.Section == "fabric" && strings.HasPrefix(receipt.Resource, prefix)
			}
		}
		if retained {
			got, exists := state.policies[want.policy.Name]
			if !exists {
				return nil, nil, errConfigurationStale
			}
			owned, err := ownedExtranetPolicy(nc.Status.Configuration, config.Name, string(config.UID), want.policy.Name, got.ID, time.Now())
			if err != nil {
				return nil, nil, err
			}
			if !owned {
				return nil, nil, errConfigurationOwned
			}
		}
		var write *configurationWrite
		var bound []contract.GroupTargetIdentity
		if got, exists := state.policies[want.policy.Name]; exists && !extranetPolicyMatches(state.desired, got) {
			write, bound, err = a.planManagedOwnedExtranetExpansion(ctx, config, source, want)
		} else {
			write, bound, err = a.client.planManagedExtranetCreate(ctx, a.statusReader, config.Namespace, want)
		}
		if err != nil {
			return nil, nil, err
		}
		if want.policy.Name == name || name == "" && selected == nil && write != nil {
			found, selected, targets = true, write, bound
		}
	}
	if !found {
		return nil, nil, errConfigurationStale
	}
	return selected, targets, nil
}

func supportedExtranetPreparation(config *configapi.NetworkControllerConfig) bool {
	return config != nil && supportedConfigurationApply(config) && len(config.Spec.ManagedSections) == 1 && config.Spec.ManagedSections[0] == "fabric" && len(config.Spec.SecretRefs) == 0
}

// Only preparation is persisted. The group coordinator still owns reservations,
// all-member claims, one-shot dispatch, uncertainty and verified settlement.
func (a *adapter) prepareExtranetPolicy(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string) (*cvk.NetworkControllerConfigurationOperation, error) {
	if a == nil || a.client == nil || !supportedExtranetPreparation(config) || !validProfileName(name) {
		return nil, errConfigurationTransition
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
	write, targets, err := a.planDeclaredExtranet(ctx, config, source, name)
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

func (a *adapter) revalidateExtranetOperation(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, op *cvk.NetworkControllerConfigurationOperation, phase string) error {
	if a == nil || a.client == nil || !supportedExtranetPreparation(config) || op == nil || (phase != "Prepared" && phase != "Submitting") || op.Phase != phase || op.Section != "fabric" || op.TargetSerial != "" || op.ParentID != "" || op.TaskID != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || op.DependencyVersions != nil || (phase == "Prepared" && op.SubmittedAt != nil) || (phase == "Submitting" && (op.SubmittedAt == nil || op.SubmittedAt.IsZero())) {
		return errConfigurationTransition
	}
	target, err := extranetOperationTarget(op.Resource, op.Action)
	if err != nil || target != op.TargetID || contract.ValidateGroupJournalIdentity(op) != nil {
		return errConfigurationStale
	}
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		write, targets, err := a.planDeclaredExtranet(ctx, config, source, name)
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
