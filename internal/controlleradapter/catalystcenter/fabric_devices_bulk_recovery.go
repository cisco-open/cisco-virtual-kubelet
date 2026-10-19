// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"errors"
	"reflect"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	contract "github.com/cisco/virtual-kubelet-cisco/internal/controllerconfiguration"
)

var errFabricDevicesConvergenceUnavailable = errors.New("bulk fabric-device convergence verifier is unavailable")

// Recover incoming declarations separately from existing peers in the retained
// physical footprint. Legacy records are accepted only when selection is unique.
func (a *adapter) retainedFabricDevicesBulk(ctx context.Context, source *configSourceSnapshot, op *cvk.NetworkControllerConfigurationOperation) ([]fabricDeviceIntent, error) {
	if source == nil || op == nil || op.Section != "fabric" || op.TargetID != op.ParentID || contract.ValidateGroupJournalIdentity(op) != nil {
		return nil, errConfigurationStale
	}
	count, _, ids, err := parseFabricDevicesBulkResourceIDs(op.Resource, op.Action)
	if err != nil {
		return nil, err
	}
	wants, err := parseFabricDeviceIntents(source.raw)
	if err != nil {
		return nil, err
	}
	inventory, err := a.client.configurationDevices(ctx)
	if err != nil {
		return nil, err
	}
	physical := map[string]string{}
	for _, target := range op.PhysicalGroup.Targets {
		physical[target.InventoryID] = target.PhysicalIdentity
	}
	selected := map[string]bool{}
	for _, id := range ids {
		if physical[id] == "" {
			return nil, errConfigurationStale
		}
		selected[id] = true
	}
	result := []fabricDeviceIntent{}
	seen := map[string]bool{}
	rootCohorts := map[string]bool{}
	for _, want := range wants {
		if want.fabricZone == "" {
			rootCohorts[want.fabricSite] = true
		}
	}
	var roots map[string]string
	for _, want := range wants {
		target, err := resolveConfigurationDevice(want.inventory.reference, inventory)
		if err != nil {
			return nil, err
		}
		if ids != nil && !selected[target.ID] || ids == nil && physical[target.ID] == "" {
			continue
		}
		canonical, err := bindMulticastRPDevices([]string{target.ID}, inventory)
		if err != nil {
			return nil, err
		}
		if seen[target.ID] || canonical[0].Serial != physical[target.ID] {
			return nil, errConfigurationStale
		}
		seen[target.ID] = true
		if want.fabricZone != "" && rootCohorts[want.fabricSite] {
			if roots == nil {
				sites, err := a.client.ListSites(ctx)
				if err != nil {
					return nil, err
				}
				fabrics, err := a.client.fabricSites(ctx)
				if err != nil {
					return nil, err
				}
				roots = map[string]string{}
				for _, site := range sites {
					if fabric, exists := fabrics[site.ID]; exists {
						roots[site.NameHierarchy] = fabric.ID
					}
				}
			}
			if roots[want.fabricSite] == op.ParentID {
				want.fabricZone = ""
			}
		}
		result = append(result, want)
	}
	if len(result) != count {
		return nil, errConfigurationStale
	}
	return result, declaredFabricDevicesBulk(source, result)
}

func (a *adapter) beginFabricDevicesBulkVerification(ctx context.Context, config *configapi.NetworkControllerConfig, source *configSourceSnapshot, members []*ops.ControllerConfigurationReservation, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) error {
	if a == nil || a.client == nil || config == nil || authorize == nil || now.IsZero() {
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
	if op.SubmittedAt == nil || op.SubmittedAt.IsZero() || !validAPIID(op.TaskID) {
		return errConfigurationTransition
	}
	wants, err := a.retainedFabricDevicesBulk(ctx, source, op)
	if err != nil {
		return err
	}
	return contract.BeginGroupVerification(ctx, a.statusReader, a.statusWriter, nc, members, func(ctx context.Context, current []*ops.ControllerConfigurationReservation) error {
		if err := authorize(ctx, current); err != nil {
			return err
		}
		ready, err := a.observeOwnedConfigurationOperation(ctx, config, source, op, "fabric", func(ctx context.Context) (bool, error) {
			done, err := a.client.configurationTaskTreeComplete(ctx, op.TaskID, op.SubmittedAt.Time, now)
			if err != nil || !done {
				return false, err
			}
			observation := op.DeepCopy()
			observation.Phase = "Verifying"
			done, err = a.client.verifyFabricDevicesBulk(ctx, wants, observation)
			if err != nil || !done {
				return false, err
			}
			before, targets, err := a.client.fabricDevicesBulkTargets(ctx, a.statusReader, config.Namespace, wants)
			if err != nil {
				return false, err
			}
			if !reflect.DeepEqual(targets, op.PhysicalGroup.Targets) {
				return false, errConfigurationStale
			}
			fresh, currentTargets, err := a.client.fabricDevicesBulkTargets(ctx, a.statusReader, config.Namespace, wants)
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
