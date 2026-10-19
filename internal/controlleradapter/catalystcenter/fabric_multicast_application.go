// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"slices"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Verify the native action and record ownership atomically through the shared
// group contract. Public dispatch requires the explicitly configured group
// runtime and live all-member admission.
func (a *adapter) recordFabricMulticastVNApplication(ctx context.Context, config *configapi.NetworkControllerConfig, operationID string, path, name string, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
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
	selected, err := a.declaredFabricMulticastVN(ctx, source, path, name)
	if err != nil {
		return err
	}

	receipt, err := a.client.verifiedFabricMulticastVNReceipt(ctx, path, selected, name, op, now)
	if err != nil {
		return err
	}
	var previous *cvk.NetworkControllerConfigurationActionApplication
	for i := range nc.Status.Configuration.ActionApplications {
		old := &nc.Status.Configuration.ActionApplications[i]
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
