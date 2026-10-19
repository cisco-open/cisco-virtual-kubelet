// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Read-only recovery connects the existing scoped semantic comparison to the
// public retained-operation path. It never supplies the complete provisioning
// proof, publishes an application, releases a member or permits another write.
func (a *adapter) recoverWiredBulkNetworkEvidence(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || config == nil || source == nil || op == nil || op.Phase != "Verifying" || op.Section != "inventory" || authorize == nil || now.IsZero() || contract.ValidateGroupJournalIdentity(op) != nil {
		return errConfigurationTransition
	}
	if !a.configurationGroupAdmissionReady() {
		return errWiredBulkConvergenceUnavailable
	}
	batch, err := a.retainedWiredBulkBatch(ctx, source, op)
	if err != nil {
		return err
	}
	dependencies, err := a.wiredBulkDependencies(ctx, source)
	if err != nil {
		return err
	}
	completed, err := a.client.observeWiredBulkProvisionCompletion(ctx, batch.path, batch.members, op, now, dependencies...)
	if err != nil {
		return err
	}
	if completed.IsZero() {
		return errConfigurationBusy
	}
	if err := a.observeWiredBulkDeclaredNetwork(ctx, config, source, batch, op, members, authorize, completed, now); err != nil {
		if errors.Is(err, errWiredBulkNetworkContextUnavailable) {
			return errWiredBulkConvergenceUnavailable
		}
		return err
	}
	// Declared network scalars do not cover switching templates, write-only
	// credentials or the complete rendered footprint. Keep every fence retained.
	return errWiredBulkConvergenceUnavailable
}
