// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Recover a recorded task using read-only native evidence. The shared contract
// retains claims and permits only Accepted/Unknown -> Verifying. This does not
// publish completion, release a reservation or attest on-device convergence.
func (a *adapter) beginFabricVLANSSIDVerification(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || authorize == nil {
		return errConfigurationTransition
	}
	want, err := a.declaredFabricVLANSSIDs(ctx, source, path)
	if err != nil {
		return err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return errConfigurationTransition
	}
	op := nc.Status.Configuration.Operation.DeepCopy()
	if _, err := parseFabricVLANSSIDResource(op.Resource); err != nil {
		return errConfigurationTransition
	}
	return contract.BeginGroupVerification(ctx, a.statusReader, a.statusWriter, nc, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := authorize(ctx, current); err != nil {
			return err
		}
		ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
			done, err := a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
			if err != nil || !done {
				return false, err
			}
			owned, err := a.currentFabricVLANSSIDOperationOwnership(ctx, config, path, op)
			if err != nil {
				return false, err
			}
			deps, targets, err := a.client.vlanSSIDTargets(ctx, a.statusReader, config.Namespace, path, want, owned)
			if err != nil {
				return false, err
			}
			if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
				return false, errConfigurationStale
			}

			// Reuse the native readback verifier without changing the persisted
			// operation. Only BeginGroupVerification may perform the transition.
			observation := op.DeepCopy()
			observation.Phase = "Verifying"
			receipt, err := a.client.verifiedFabricVLANSSIDReceipt(ctx, path, want, owned, observation, now)
			if err != nil || receipt == nil {
				return false, err
			}
			fresh, freshTargets, err := a.client.vlanSSIDTargets(ctx, a.statusReader, config.Namespace, path, want, owned)
			if err != nil {
				return false, err
			}
			if fresh.digest != deps.digest || !reflect.DeepEqual(targets, freshTargets) {
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
	}, now)
}
