// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

var errWiredBulkConvergenceUnavailable = errors.New("bulk provisioning device convergence verifier is unavailable")

// Reconstruct the exact retained footprint for dispatch or acknowledged recovery,
// never the remaining unprovisioned
// subset. Selection uses source declarations and retained native/physical IDs;
// grouping options cannot retarget an accepted task after restart.
func (a *adapter) retainedWiredBulkBatch(ctx context.Context, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation) (wiredBulkBatch, error) {
	var empty wiredBulkBatch
	if a == nil || a.client == nil || source == nil || op == nil || contract.ValidateGroupJournalIdentity(op) != nil || op.Section != "inventory" || op.TargetID != op.ParentID {
		return empty, errConfigurationStale
	}
	count, mask, _, err := parseWiredBulkResource(op.Resource, op.Action)
	if err != nil || count != len(op.PhysicalGroup.Targets) {
		return empty, errConfigurationStale
	}
	intent, states, _, err := parseWiredBulkSource(source)
	if err != nil {
		return empty, err
	}
	sites, err := a.client.ListSites(ctx)
	if err != nil {
		return empty, err
	}
	path := ""
	for _, site := range sites {
		if site.ID == op.ParentID {
			if path != "" {
				return empty, errConfigurationStale
			}
			path = site.NameHierarchy
		}
	}
	if !validSitePath(path) {
		return empty, errConfigurationStale
	}
	inventory, err := a.client.configurationDevices(ctx)
	if err != nil {
		return empty, err
	}
	expected := map[string]string{}
	for _, target := range op.PhysicalGroup.Targets {
		expected[target.InventoryID] = target.PhysicalIdentity
	}
	state := "PROVISION"
	if op.Action == "Update" {
		state = "REPROVISION"
	}
	batch := wiredBulkBatch{path: path}
	seen := map[string]bool{}
	for _, want := range intent.devices {
		if states[want.reference.Name] == "" {
			continue
		}
		native, err := resolveConfigurationDevice(want.reference, inventory)
		if err != nil {
			return empty, err
		}
		serial, selected := expected[native.ID]
		if !selected {
			continue
		}
		physical, err := bindMulticastRPDevices([]string{native.ID}, inventory)
		if err != nil {
			return empty, err
		}
		if seen[native.ID] || physical[0].Serial != serial || states[want.reference.Name] != state || !siteWithin(want.site, path) {
			return empty, errConfigurationStale
		}
		seen[native.ID] = true
		batch.members = append(batch.members, wiredBulkMember{desired: want, state: state})
	}
	if len(batch.members) != count {
		return empty, errConfigurationStale
	}
	if err := declaredWiredBulkBatch(source, batch); err != nil {
		return empty, err
	}
	if err := a.client.checkWiredBulkNetworkContext(ctx, source, []wiredBulkBatch{batch}); err != nil {
		return empty, err
	}
	dependencies, err := a.wiredBulkDependencies(ctx, source)
	if err != nil {
		return empty, err
	}
	if len(dependencies) != 0 || strings.HasPrefix(op.Resource, wiredBulkProvisionPrefix+"v2:") {
		native, err := a.client.wiredBulkSnapshot(ctx, batch.path, batch.members, dependencies...)
		if err != nil {
			return empty, err
		}
		if native.resource(mask) != op.Resource || native.anchor != op.ParentID {
			return empty, errConfigurationStale
		}
	}
	return batch, nil
}

// Read-only Accepted/Unknown-with-task -> Verifying via the existing complete
// claim/receipt CAS. No final convergence receipt, release or resubmission.
func (a *adapter) beginWiredBulkVerification(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || a.client == nil || config == nil || !supportedConfigurationApply(config) || source == nil || authorize == nil || now.IsZero() {
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
	if op.Section != "inventory" || !validConfigurationGroupAction(op.Section, op.Resource, op.Action) {
		return errConfigurationTransition
	}
	batch, err := a.retainedWiredBulkBatch(ctx, source, op)
	if err != nil {
		return err
	}
	dependencies, err := a.wiredBulkDependencies(ctx, source)
	if err != nil {
		return err
	}
	return contract.BeginGroupVerification(ctx, a.statusReader, a.statusWriter, nc, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := authorize(ctx, current); err != nil {
			return err
		}
		ready, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "inventory", func(ctx context.Context) (bool, error) {
			// Only the shared contract may mutate the persisted phase.
			observation := op.DeepCopy()
			observation.Phase = "Verifying"
			done, err := a.client.verifyWiredBulkProvision(ctx, batch.path, batch.members, observation, now, dependencies...)
			if err != nil || !done {
				return false, err
			}
			before, targets, err := a.client.wiredBulkTargets(ctx, a.statusReader, config.Namespace, batch.path, batch.members, dependencies...)
			if err != nil {
				return false, err
			}
			if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
				return false, errConfigurationStale
			}
			fresh, currentTargets, err := a.client.wiredBulkTargets(ctx, a.statusReader, config.Namespace, batch.path, batch.members, dependencies...)
			if err != nil {
				return false, err
			}
			if !reflect.DeepEqual(before, fresh) || !reflect.DeepEqual(targets, currentTargets) {
				return false, errConfigurationStale
			}
			return true, nil
		})
		if err != nil {
			return err
		}
		if !ready {
			return errConfigurationBusy
		}
		return nil
	}, now)
}
