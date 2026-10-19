// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"slices"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
)

// Staged group recovery entry point. It verifies the native action and records
// ownership atomically through the shared contract. The public reconciler still
// refuses group operations until installed admission and actors are enabled.
func (a *adapter) recordFabricSDAHandoffApplication(ctx context.Context, config *configapi.NetworkControllerConfig, operationID string, key fabricSDAHandoffIntent, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || !slices.Contains(config.Spec.ManagedSections, "fabric") || len(config.Spec.SecretRefs) != 0 || !configurationOperationID.MatchString(operationID) || authorize == nil || now.IsZero() {
		return errConfigurationTransition
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		return err
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
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
	if op.ID != operationID || op.Phase != "Verifying" || op.Section != "fabric" || op.ConfigName != config.Name || op.ConfigUID != string(config.UID) || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return errConfigurationStale
	}
	selected, err := declaredFabricSDAHandoff(source, key)
	if err != nil {
		return err
	}

	receipt, err := a.client.verifiedFabricSDAHandoffReceipt(ctx, *selected, op, now)
	if err != nil {
		return err
	}
	return a.persistVerifiedConfigurationGroupReceipt(ctx, config, source, nc, members, receipt, authorize, now)
}
