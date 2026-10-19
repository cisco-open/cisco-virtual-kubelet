// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func isExtranetGroupResource(section, resource string) bool {
	return section == "fabric" && (strings.HasPrefix(resource, "extranet-create:") || strings.HasPrefix(resource, "extranet-update:") || strings.HasPrefix(resource, "extranet-update-v2:"))
}

// Observe acknowledged native results under the existing source/owner/journal
// bracket. This does not advance a phase, publish a receipt or release a claim.
func (a *adapter) observeExtranetRecoveryResult(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, op *cvk.NetworkControllerConfigurationOperation, now time.Time) (bool, error) {
	if a == nil || a.client == nil || !supportedExtranetPreparation(config) || op == nil || (op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying") || op.SubmittedAt == nil || !validAPIID(op.TaskID) || now.IsZero() || op.ParentID != "" || op.TargetSerial != "" || op.CreatedResourceID != "" || op.CancellationRequestedAt != nil || len(op.CredentialVersions) != 0 || op.DependencyVersions != nil || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) || contract.ValidateGroupJournalIdentity(op) != nil {
		return false, errConfigurationTransition
	}
	target, err := extranetOperationTarget(op.Resource, op.Action)
	if err != nil || target != op.TargetID {
		return false, errConfigurationStale
	}
	wants, err := a.declaredExtranetFoundation(ctx, source)
	if err != nil {
		return false, err
	}
	var selected *fabricExtranetIntent
	for i := range wants {
		if wants[i].policy.Name == name {
			selected = &wants[i]
		}
	}
	if selected == nil {
		return false, errConfigurationStale
	}
	return a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		if op.Action == "Update" {
			nc, err := a.currentController(ctx)
			if err != nil {
				return false, err
			}
			owned, err := ownedExtranetPolicy(nc.Status.Configuration, config.Name, string(config.UID), name, op.TargetID, now)
			if err != nil || !owned {
				if err == nil {
					err = errConfigurationOwned
				}
				return false, err
			}
		}
		done, err := a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		if err != nil || !done {
			return false, err
		}
		before, targets, err := a.client.extranetManagedTargets(ctx, a.statusReader, config.Namespace, *selected)
		if err != nil {
			return false, err
		}
		if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return false, errConfigurationStale
		}
		observation := op.DeepCopy()
		observation.Phase = "Verifying"
		receipt, err := a.client.verifiedExtranetReceipt(ctx, *selected, observation, now)
		if err != nil || receipt == nil {
			return false, err
		}
		after, fresh, err := a.client.extranetManagedTargets(ctx, a.statusReader, config.Namespace, *selected)
		if err != nil {
			return false, err
		}
		if before.digest != after.digest || !reflect.DeepEqual(before.policies, after.policies) || !reflect.DeepEqual(targets, fresh) {
			return false, errConfigurationStale
		}
		// Repeat the complete tree after native/managed observations so a late
		// child failure cannot be hidden by an earlier successful root query.
		return a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
	})
}

func (a *adapter) beginExtranetVerification(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || authorize == nil || !supportedExtranetPreparation(config) {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	return contract.BeginGroupVerification(ctx, a.statusReader, a.statusWriter, nc, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := authorize(ctx, current); err != nil {
			return err
		}
		ok, err := a.observeExtranetRecoveryResult(ctx, config, source, name, op, now)
		if err != nil {
			return err
		}
		if !ok {
			return errConfigurationBusy
		}
		return nil
	}, now)
}

// Existing atomic publication remains the sole receipt/Verified writer. Public
// recovery adds task-tree and repeated managed-result evidence before invoking it.
func (a *adapter) recordRecoveredExtranetApplication(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, name string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || authorize == nil {
		return errConfigurationTransition
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Verifying" {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	ok, err := a.observeExtranetRecoveryResult(ctx, config, source, name, op, now)
	if err != nil {
		return err
	}
	if !ok {
		return errConfigurationBusy
	}
	return a.recordExtranetApplication(ctx, config, op.ID, name, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := authorize(ctx, current); err != nil {
			return err
		}
		// Publication authorization can race the last observation. Recheck the
		// whole tree here, before the shared atomic writer may publish Verified.
		done, err := a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
		if err != nil {
			return err
		}
		if !done {
			return errConfigurationBusy
		}
		return nil
	}, now)
}

func (r *configReconciler) recoverExtranetGroup(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error) error {
	wants, err := r.a.declaredExtranetFoundation(ctx, source)
	if err != nil {
		return err
	}
	resource := op.Resource
	if op.Action == "Update" {
		resource, err = extranetUpdateNativeResource(resource)
		if err != nil {
			return err
		}
	}
	name := ""
	for _, want := range wants {
		state, err := r.a.client.extranetSnapshot(ctx, want)
		if err != nil {
			return err
		}
		expected := state.resource()
		if op.Action == "Update" {
			expected = state.updateResource(op.TargetID)
		}
		if expected == resource {
			if name != "" {
				return errConfigurationStale
			}
			name = want.policy.Name
		}
	}
	if name == "" {
		return errConfigurationStale
	}
	switch op.Phase {
	case "Prepared":
		if err := r.a.revalidateExtranetOperation(ctx, config, source, name, op, "Prepared"); err != nil {
			return err
		}
		permit, err := r.a.claimConfigurationGroup(ctx)
		if err != nil {
			return err
		}
		_, err = r.a.dispatchExtranetPolicy(ctx, config, source, name, permit)
		return err
	case "Verifying":
		return r.a.recordRecoveredExtranetApplication(ctx, config, source, name, members, authorize, time.Now())
	default:
		return r.a.beginExtranetVerification(ctx, config, source, name, members, authorize, time.Now())
	}
}
