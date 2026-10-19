// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
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

// Private bridge to the existing atomic group receipt/retirement contract. It
// does not submit, claim, release, settle or clear a journal. Public activation
// requires installed all-member authority and a real convergence proof provider.
func (a *adapter) recordWiredBulkApplication(ctx context.Context, config *configapi.NetworkControllerConfig, operationID string, batch wiredBulkBatch, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, proof wiredBulkConvergenceProof, now time.Time) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" || len(config.Spec.SecretRefs) != 0 || !configurationOperationID.MatchString(operationID) || authorize == nil || proof == nil || now.IsZero() || !a.configurationGroupAdmissionReady() {
		return errConfigurationDeviceAdmission
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil {
		return err
	}
	if err := declaredWiredBulkBatch(source, batch); err != nil {
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
	if op.ID != operationID || op.Phase != "Verifying" || op.Section != "inventory" || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != nc.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version {
		return errConfigurationStale
	}
	var provedCompleted time.Time
	guardedProof := func(ctx context.Context, operation *cvk.NetworkControllerConfigurationOperation, targets []cvk.NetworkControllerConfigurationGroupTarget, completed time.Time) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		if err := a.client.checkWiredBulkNetworkContext(ctx, source, []wiredBulkBatch{batch}); err != nil {
			return err
		}
		if err := proof(ctx, operation, targets, completed); err != nil {
			return err
		}
		provedCompleted = completed
		if err := a.client.checkWiredBulkNetworkContext(ctx, source, []wiredBulkBatch{batch}); err != nil {
			return err
		}
		return a.checkConfigurationSnapshot(ctx, config, source)
	}
	dependencies, err := a.wiredBulkDependencies(ctx, source)
	if err != nil {
		return err
	}
	receipt, err := a.client.verifiedWiredBulkReceipt(ctx, a.statusReader, config.Namespace, batch, op, now, guardedProof, dependencies...)
	if err != nil {
		return err
	}
	finalAuthorize := authorize
	if len(dependencies) > 0 {
		// Authorization can itself observe changing state. Recheck the exact
		// allocation and task incarnation after it, before publishing a receipt.
		finalAuthorize = func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
			if err := authorize(ctx, current); err != nil {
				return err
			}
			if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
				return err
			}
			completed, err := a.client.observeWiredBulkProvisionCompletion(ctx, batch.path, batch.members, op, now, dependencies...)
			if err != nil {
				return err
			}
			if provedCompleted.IsZero() || !completed.Equal(provedCompleted) {
				return errConfigurationStale
			}
			_, targets, err := a.client.wiredBulkTargets(ctx, a.statusReader, config.Namespace, batch.path, batch.members, dependencies...)
			if err != nil {
				return err
			}
			expected, err := contract.CanonicalGroupTargets(op.PhysicalGroup.Targets)
			if err != nil || !reflect.DeepEqual(targets, expected) {
				return errConfigurationStale
			}
			return a.checkConfigurationSnapshot(ctx, config, source)
		}
	}
	var previous *cvk.NetworkControllerConfigurationActionApplication
	for i := range nc.Status.Configuration.ActionApplications {
		old := &nc.Status.Configuration.ActionApplications[i]
		if old.Section == receipt.Section && old.Resource == receipt.Resource {
			if previous != nil {
				return errConfigurationOwned
			}
			previous = old.DeepCopy()
		}
	}
	if previous != nil {
		return a.persistVerifiedConfigurationGroupReceipt(ctx, config, source, nc, members, receipt, finalAuthorize, now, previous)
	}
	return a.persistVerifiedConfigurationGroupReceipt(ctx, config, source, nc, members, receipt, finalAuthorize, now)
}
