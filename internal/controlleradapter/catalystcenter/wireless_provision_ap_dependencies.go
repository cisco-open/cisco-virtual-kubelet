// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"reflect"
	"strings"
	"time"
)

type wirelessProvisionAPDependency struct {
	prefix   string
	executor *wirelessManagedAPExecutor
}

// Readback supplies exactly the same digest used by the AP assignment journal.
// Task acceptance, or a receipt for an older placement, cannot establish this.
func (e *wirelessProvisionExecutor) requiredAPReceipts(ctx context.Context) ([]string, error) {
	resources := append([]string(nil), e.dependencyResources...)
	for _, dependency := range e.apDependencies {
		if dependency.executor == nil || dependency.executor.reference != e.reference || dependency.prefix == "" || !strings.HasSuffix(dependency.prefix, "/") {
			return nil, errConfigurationTransition
		}
		row, err := dependency.executor.observe(ctx)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(row.desired, row.actual) {
			return nil, errConfigurationBusy
		}
		resource := dependency.prefix + row.resource
		if len(resource) > 512 {
			return nil, errConfigurationTransition
		}
		resources = append(resources, resource)
	}
	return resources, nil
}

// Recheck dependencies at native submission, after the shared journal and
// reservation permit have admitted the write. This supplies no permit itself.
func (e *wirelessProvisionExecutor) guardAPDependencies(write *configurationWrite, resources []string) *configurationWrite {
	if write == nil {
		return nil
	}
	guarded := *write
	expected := append([]string(nil), resources...)
	submit := write.submit
	guarded.submit = func(ctx context.Context) (Task, error) {
		if submit == nil {
			return Task{}, errConfigurationTransition
		}
		if err := e.adapter.checkConfigurationSnapshot(ctx, e.config, e.source); err != nil {
			return Task{}, err
		}
		if e.validateDependencies != nil {
			if err := e.validateDependencies(ctx); err != nil {
				return Task{}, err
			}
		}
		current, err := e.requiredAPReceipts(ctx)
		if err != nil {
			return Task{}, err
		}
		if !reflect.DeepEqual(current, expected) {
			return Task{}, errConfigurationStale
		}
		nc, err := e.adapter.currentController(ctx)
		if err != nil {
			return Task{}, err
		}
		if err := e.dependenciesSettled(ctx, nc, current, false, time.Now()); err != nil {
			return Task{}, err
		}
		return submit(ctx)
	}
	return &guarded
}

// Existing matching AP assignments need no fabricated mutation receipt. The
// reservation is created after the Prepared journal and its dependency readback;
// its creation timestamp supplies a restart-stable minimum settling interval.
// This fallback is available only for dependencies observed by AP executors.
func (e *wirelessProvisionExecutor) dependenciesSettled(ctx context.Context, nc *cvk.NetworkController, resources []string, allowPreparation bool, now time.Time) error {
	pending := false
	seen := map[string]bool{}
	for _, resource := range resources {
		if seen[resource] {
			return errConfigurationTransition
		}
		seen[resource] = true
		err := wirelessProvisionDependenciesReady(nc, e.config, e.source, []string{resource}, now)
		if err == nil {
			continue
		}
		if !errors.Is(err, errConfigurationBusy) {
			return err
		}
		pending = true
	}
	if !pending {
		return nil
	}
	if len(e.dependencyResources) != 0 || len(resources) == 0 || len(resources) != len(e.apDependencies) {
		return errConfigurationBusy
	}
	if nc == nil {
		return errConfigurationStale
	}
	if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		if allowPreparation {
			return nil
		}
		return errConfigurationBusy
	}
	op := nc.Status.Configuration.Operation
	if op.Phase != "Prepared" && op.Phase != "Submitting" {
		return errConfigurationBusy
	}
	reservation, err := e.adapter.configurationReservation(ctx, nc)
	if err != nil {
		return err
	}
	if reservation.DeletionTimestamp != nil || reservation.Status.PreparationCancelledAt != nil || reservation.Status.ManagerSettledAt != nil || reservation.Status.Completion != nil || reservation.Status.Grant != nil && reservation.Status.Grant.Phase != "Held" {
		return errConfigurationStale
	}
	if reservation.CreationTimestamp.IsZero() || reservation.CreationTimestamp.After(now) {
		return errConfigurationStale
	}
	if now.Before(reservation.CreationTimestamp.Add(wirelessManagedAPSettleDelay + time.Second)) {
		return errConfigurationBusy
	}
	return nil
}
