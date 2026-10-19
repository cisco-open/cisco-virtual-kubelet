// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

func (a *adapter) beginFabricAuthenticationVerification(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, path string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || authorize == nil {
		return errConfigurationTransition
	}
	want, err := a.declaredFabricAuthentication(ctx, source, path)
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
	return contract.BeginGroupVerification(ctx, a.statusReader, a.statusWriter, nc, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := authorize(ctx, current); err != nil {
			return err
		}
		ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
			before, targets, err := a.client.fabricAuthenticationTargets(ctx, a.statusReader, config.Namespace, want)
			if err != nil {
				return false, err
			}
			if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
				return false, errConfigurationStale
			}
			observation := op.DeepCopy()
			observation.Phase = "Verifying"
			if _, err := a.client.verifiedFabricAuthenticationReceipt(ctx, want, observation, now); err != nil {
				return false, err
			}
			after, fresh, err := a.client.fabricAuthenticationTargets(ctx, a.statusReader, config.Namespace, want)
			if err != nil {
				return false, err
			}
			if before.observation != after.observation || !reflect.DeepEqual(targets, fresh) {
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

func (a *adapter) recordFabricAuthenticationApplication(ctx context.Context, config *configapi.NetworkControllerConfig, operationID, path string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "fabric" || len(config.Spec.SecretRefs) != 0 || !configurationOperationID.MatchString(operationID) || authorize == nil || now.IsZero() {
		return errConfigurationTransition
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
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
	op := nc.Status.Configuration.Operation
	if op.ID != operationID || op.Phase != "Verifying" {
		return errConfigurationStale
	}
	want, err := a.declaredFabricAuthentication(ctx, source, path)
	if err != nil {
		return err
	}
	var receipt *cvk.NetworkControllerConfigurationActionApplication
	ok, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
		_, targets, err := a.client.fabricAuthenticationTargets(ctx, a.statusReader, config.Namespace, want)
		if err != nil {
			return false, err
		}
		if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
			return false, errConfigurationStale
		}
		receipt, err = a.client.verifiedFabricAuthenticationReceipt(ctx, want, op, now)
		return err == nil && receipt != nil, err
	})
	if err != nil {
		return err
	}
	if !ok || receipt == nil {
		return errConfigurationBusy
	}
	var previous *cvk.NetworkControllerConfigurationActionApplication
	for _, old := range nc.Status.Configuration.ActionApplications {
		if old.Section == receipt.Section && old.Resource == receipt.Resource {
			if previous != nil {
				return errConfigurationStale
			}
			previous = old.DeepCopy()
		}
	}
	if previous != nil {
		return a.persistVerifiedConfigurationGroupReceipt(ctx, config, source, nc, members, receipt, authorize, now, previous)
	}
	return a.persistVerifiedConfigurationGroupReceipt(ctx, config, source, nc, members, receipt, authorize, now)
}
