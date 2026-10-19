// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"reflect"

	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

type wiredBulkBatchObservation struct {
	native  *wiredBulkSnapshot
	targets []contract.GroupTargetIdentity
}
type wiredBulkPlannedBatch struct {
	batch   wiredBulkBatch
	write   *configurationWrite
	targets []contract.GroupTargetIdentity
}
type wiredBulkBatchGuard func(context.Context, wiredBulkBatch, []Device) error

// Preflight the entire batch sequence before exposing any submission closure.
// No selected late batch may hide an unresolved prerequisite or physical alias.
// The caller must replan after each settled native mutation, select by durable
// ownership/receipt evidence and use the shared journal. This is not a retry or
// settlement loop and does not derive reprovision completion from native presence.
func (c *client) planManagedWiredBulkSequence(ctx context.Context, reader ctrlclient.Reader, namespace string, batches []wiredBulkBatch, guard wiredBulkBatchGuard, dependencies ...wiredBulkDependencyObserver) ([]wiredBulkPlannedBatch, error) {
	if len(batches) == 0 || len(batches) > 1000 {
		return nil, errConfigurationTransition
	}
	collect := func(ctx context.Context) ([]wiredBulkBatchObservation, error) {
		observations := make([]wiredBulkBatchObservation, 0, len(batches))
		ids, physical := map[string]bool{}, map[string]bool{}
		for _, batch := range batches {
			native, targets, err := c.wiredBulkTargets(ctx, reader, namespace, batch.path, batch.members, dependencies...)
			if err != nil {
				return nil, err
			}
			for _, target := range targets {
				if ids[target.InventoryID] || physical[target.PhysicalIdentity] {
					return nil, errConfigurationOwned
				}
				ids[target.InventoryID], physical[target.PhysicalIdentity] = true, true
			}
			observations = append(observations, wiredBulkBatchObservation{native, targets})
		}
		return observations, nil
	}
	before, err := collect(ctx)
	if err != nil {
		return nil, err
	}
	planned := make([]wiredBulkPlannedBatch, 0, len(batches))
	for i, batch := range batches {
		observation := before[i]
		if observation.native.mask == 0 {
			planned = append(planned, wiredBulkPlannedBatch{batch: batch, targets: observation.targets})
			continue
		}
		if guard == nil {
			return nil, errConfigurationDeviceAdmission
		}
		// Native/managed all-batch evidence must remain intact around authority.
		check := func(ctx context.Context) error {
			fresh, err := collect(ctx)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(before, fresh) {
				return errConfigurationStale
			}
			return nil
		}
		write, targets, err := c.planManagedWiredBulkProvision(ctx, reader, namespace, batch.path, batch.members, func(ctx context.Context, devices []Device) error {
			if err := check(ctx); err != nil {
				return err
			}
			if err := guard(ctx, batch, devices); err != nil {
				return err
			}
			return check(ctx)
		}, dependencies...)
		if err != nil {
			return nil, err
		}
		if write == nil || write.resource != observation.native.resource(observation.native.mask) || !reflect.DeepEqual(targets, observation.targets) {
			return nil, errConfigurationStale
		}
		planned = append(planned, wiredBulkPlannedBatch{batch: batch, write: write, targets: targets})
	}
	// Planning all native closures can itself race a later batch's bindings.
	fresh, err := collect(ctx)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(before, fresh) {
		return nil, errConfigurationStale
	}
	return planned, nil
}
