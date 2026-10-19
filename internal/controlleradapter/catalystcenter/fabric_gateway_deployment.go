// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"slices"
	"strings"
)

type fabricGatewayDeployment struct {
	foundation       configurationExecutor
	foundationReport func(context.Context) ([]configapi.NetworkControllerDriftEntry, error)
	gateways         *anycastGatewayExecutor
	ipam             *ipamConfigurationExecutor
	sites            []fabricSiteIntent
	zones            []fabricZoneIntent
	definitions      []string
	associations     []fabricL3AssociationIntent
}

func (e *fabricGatewayDeployment) count() int { return e.foundation.count() + e.gateways.count() }
func (e *fabricGatewayDeployment) preflight(ctx context.Context, apply bool) ([]configapi.NetworkControllerDriftEntry, error) {
	inventory, err := e.gateways.client.loadAnycastGatewayInventory(ctx, e.gateways.desired)
	if err != nil {
		return nil, err
	}
	var poolPreview *anycastPoolPreviewInventory
	if e.ipam != nil {
		poolPreview, err = e.ipam.loadAnycastPoolPreview(ctx)
		if err != nil {
			return nil, err
		}
	}
	paths := map[string]bool{}
	for _, s := range e.sites {
		paths[s.path] = true
	}
	for _, z := range e.zones {
		paths[z.path] = true
	}
	members := map[fabricL3AssociationIntent]bool{}
	for _, a := range e.associations {
		members[a] = true
	}
	pending := map[anycastGatewayKey]bool{}
	for _, w := range e.gateways.desired {
		state := inventory.states[fabricL3AssociationIntent{path: w.path, parent: w.parent, name: w.network}]
		if state.fabricID != "" {
			pending[anycastGatewayKey{state.fabricID, w.pool}] = true
		}
	}
	drift := []configapi.NetworkControllerDriftEntry{}
	for _, w := range e.gateways.desired {
		ref := fabricL3AssociationIntent{path: w.path, parent: w.parent, name: w.network}
		state, known := inventory.states[ref]
		if !known {
			return nil, errInvalidResponse
		}
		if state.fabricID == "" && !paths[w.path] || state.network.ID == "" && !slices.Contains(e.definitions, w.network) || !state.exists && !members[ref] {
			return nil, errConfigurationBusy
		}
		if w.parent != "" && !slices.Contains(state.network.Fabrics, state.parentFabricID) && !members[fabricL3AssociationIntent{path: w.parent, name: w.network}] {
			return nil, errConfigurationBusy
		}
		ownerPath := w.path
		if w.parent != "" {
			ownerPath = w.parent
		}
		owner, ok := inventory.hierarchy[ownerPath]
		if !ok {
			return nil, errInvalidResponse
		}
		ownerID := owner.ID
		if state.network.Anchor != "" {
			anchor, ok := inventory.fabrics[state.network.Anchor]
			if !ok {
				return nil, errConfigurationStale
			}
			ownerID = anchor.SiteID
		}
		poolPending := false
		if poolPreview != nil {
			preview, err := poolPreview.preview(w.anycastGatewayIntent, ownerID)
			if err != nil {
				return nil, err
			}
			poolPending = preview.pending
		}
		if !poolPending {
			if _, err := inventory.pools.bind(w.anycastGatewayIntent, ownerID); err != nil {
				return nil, err
			}
		}
		actual, exists := inventory.gateways[anycastGatewayKey{state.fabricID, w.pool}]
		// Changing prerequisites of an existing gateway needs a separately
		// qualified transition. Do not silently mutate that live dependency.
		if poolPending && exists {
			return nil, errConfigurationTransition
		}
		if apply && !exists {
			if err := validateAnycastCreateIntent(w); err != nil {
				return nil, err
			}
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if state.ready && state.exists && !poolPending {
			deps, err := inventory.resolveWithPending(w, pending)
			if err != nil {
				return nil, err
			}
			if deps.exists {
				matches, err := anycastGatewayMatches(w.anycastGatewayIntent, deps.actual, deps.pools, deps.parent)
				if err != nil {
					return nil, err
				}
				if matches {
					continue
				}
				if apply {
					return nil, errConfigurationTransition
				}
				change = configapi.NetworkControllerDriftChangeUpdate
			}
		} else if exists || actual.ID != "" {
			return nil, errConfigurationStale
		}
		drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: strings.TrimSuffix(w.resourcePrefix(), ":"), Path: "/fabric_sites/anycast_gateways", Change: change})
	}
	return drift, nil
}
func (e *fabricGatewayDeployment) next(ctx context.Context) (*configurationWrite, error) {
	if _, err := e.preflight(ctx, true); err != nil {
		return nil, err
	}
	if e.ipam != nil {
		pending, err := e.ipam.next(ctx)
		if err != nil {
			return nil, err
		}
		// The combined executor owns IPAM writes and their journal namespace.
		if pending != nil {
			return nil, nil
		}
	}
	write, err := e.foundation.next(ctx)
	if err != nil || write != nil {
		return write, err
	}
	return e.gateways.next(ctx)
}
func (e *fabricGatewayDeployment) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if strings.HasPrefix(op.Resource, "anycast-gateway:") || isAnycastBulkResource(op.Resource) {
		if e.ipam != nil {
			pending, err := e.ipam.next(ctx)
			if err != nil {
				return false, err
			}
			if pending != nil {
				return false, errConfigurationStale
			}
		}
		pending, err := e.foundation.next(ctx)
		if err != nil {
			return false, err
		}
		if pending != nil {
			return false, errConfigurationStale
		}
		return e.gateways.verify(ctx, op)
	}
	return e.foundation.verify(ctx, op)
}
func (e *fabricGatewayDeployment) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	out, err := e.foundationReport(ctx)
	if err != nil {
		return nil, err
	}
	drift, err := e.preflight(ctx, false)
	if err != nil {
		return nil, err
	}
	return append(out, drift...), nil
}
