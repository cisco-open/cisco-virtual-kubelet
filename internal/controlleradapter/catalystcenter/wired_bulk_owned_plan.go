// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

// Private sequence bridge. Full native/managed preflight still runs for batches
// whose current owned receipt suppresses PUT. It returns native writes, not a
// persisted operation, reservation, claim, or completion decision.
func (a *adapter) planOwnedWiredBulkSequence(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, batches []wiredBulkBatch, guard wiredBulkBatchGuard, now time.Time) ([]wiredBulkPlannedBatch, error) {
	if a == nil || a.client == nil || config == nil || source == nil || guard == nil || now.IsZero() || !a.configurationGroupAdmissionReady() || !supportedConfigurationApply(config) || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" {
		return nil, errConfigurationDeviceAdmission
	}
	for _, batch := range batches {
		if err := declaredWiredBulkBatch(source, batch); err != nil {
			return nil, err
		}
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
		return nil, err
	}
	dependencies, err := a.wiredBulkDependencies(ctx, source)
	if err != nil {
		return nil, err
	}
	if err := a.client.checkWiredBulkNetworkContext(ctx, source, batches); err != nil {
		return nil, err
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		return nil, err
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation != nil {
		return nil, errConfigurationBusy
	}
	owners := 0
	for _, owner := range nc.Status.Configuration.Owners {
		if owner.Section == "inventory" {
			if owner.ConfigUID != string(config.UID) || owner.ConfigName != config.Name {
				return nil, errConfigurationOwned
			}
			owners++
		}
	}
	if owners != 1 {
		return nil, errConfigurationOwned
	}
	var planned []wiredBulkPlannedBatch
	checkSubmission := func(ctx context.Context, batch wiredBulkBatch) error {
		if err := a.checkConfigurationSnapshot(ctx, config, source); err != nil {
			return err
		}
		if err := a.client.checkWiredBulkNetworkContext(ctx, source, batches); err != nil {
			return err
		}
		current, err := a.currentController(ctx)
		if err != nil {
			return err
		}
		if current.Status.Configuration == nil || current.Status.Configuration.Operation == nil {
			return errConfigurationTransition
		}
		op := current.Status.Configuration.Operation
		if op.Phase != "Submitting" || op.ConfigUID != string(config.UID) || op.ConfigName != config.Name || op.ConfigGeneration != config.Generation || op.ControllerGeneration != current.Generation || op.SourceUID != string(source.uid) || op.SourceVersion != source.version || op.Section != "inventory" || op.TaskID != "" || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(time.Now()) || contract.ValidateGroupJournalIdentity(op) != nil {
			return errConfigurationStale
		}
		ownerCount := 0
		for _, owner := range current.Status.Configuration.Owners {
			if owner.Section == "inventory" {
				if owner.ConfigUID != op.ConfigUID || owner.ConfigName != op.ConfigName {
					return errConfigurationOwned
				}
				ownerCount++
			}
		}
		if ownerCount != 1 {
			return errConfigurationOwned
		}
		for _, entry := range planned {
			if entry.write != nil && reflect.DeepEqual(entry.batch, batch) {
				w := entry.write
				if op.Resource != w.resource || op.Action != w.action || op.TargetID != w.identity.target || op.ParentID != w.identity.parent || !reflect.DeepEqual(op.PhysicalGroup.Targets, entry.targets) {
					return errConfigurationStale
				}
				return nil
			}
		}
		return errConfigurationStale
	}
	planned, err = a.client.planManagedWiredBulkSequence(ctx, a.statusReader, config.Namespace, batches, func(ctx context.Context, batch wiredBulkBatch, devices []Device) error {
		if err := checkSubmission(ctx, batch); err != nil {
			return err
		}
		if err := guard(ctx, batch, devices); err != nil {
			return err
		}
		return checkSubmission(ctx, batch)
	}, dependencies...)
	if err != nil {
		return nil, err
	}
	for i := range planned {
		entry := &planned[i]
		if entry.write == nil {
			continue
		}
		// A comparison-only canonical operation: this zero nonce is never returned,
		// persisted or submitted. The shared journal generates the real random nonce.
		desired := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("0", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, SourceUID: string(source.uid), SourceVersion: source.version, Section: "inventory", Resource: entry.write.resource, Action: entry.write.action, TargetID: entry.write.identity.target, ParentID: entry.write.identity.parent, Phase: "Prepared"}
		desired, err = contract.WithGroupIdentity(desired, entry.targets)
		if err != nil {
			return nil, err
		}
		applied, err := wiredBulkAlreadyApplied(nc.Status.Configuration, desired, now)
		if err != nil {
			return nil, err
		}
		if applied {
			entry.write = nil
		}
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
	return planned, nil
}
