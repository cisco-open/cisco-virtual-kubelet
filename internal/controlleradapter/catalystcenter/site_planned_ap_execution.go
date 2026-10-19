// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

// Site geometry and map placements retain separate resource identities but use
// one shared configuration journal. Preflight existing placements before any
// site write; missing floors must have valid creation intent.
type sitePlannedAPExecutor struct {
	designGuard   func(context.Context, string, string, configurationResourceIdentity) error
	retainedGuard func(context.Context, string, string, configurationResourceIdentity) error
	floorHandoff  bool
	sites         *siteConfigurationExecutor
	placements    *plannedAPExecutor
	images        *floorImageExecutor
}

func (e *sitePlannedAPExecutor) count() int {
	n := e.sites.count() + e.placements.count()
	if e.images != nil {
		n += e.images.count()
	}
	return n
}
func (e *sitePlannedAPExecutor) next(ctx context.Context) (*configurationWrite, error) {
	if err := e.placements.client.validatePlannedAPModels(ctx, e.placements.desired); err != nil {
		return nil, err
	}
	observed, err := e.sites.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	// Identity ownership must be checked even when attributes already match,
	// and before planning floor images or placements against those identities.
	if err := e.sites.checkCreatorIdentities(observed); err != nil {
		return nil, err
	}
	if err := e.sites.checkPrivateBaselines(ctx, observed); err != nil {
		return nil, err
	}
	if err := e.placements.checkFloorIdentities(observed); err != nil {
		return nil, err
	}
	retained, err := e.placements.retainedPrivateView(ctx, observed)
	if err != nil {
		return nil, err
	}
	mutation, err := nextSiteMutation(e.sites.desired, observed)
	if err != nil {
		return nil, err
	}
	existing := map[string]Site{}
	for _, site := range observed {
		existing[site.NameHierarchy] = site
	}
	available := []plannedAPIntent{}
	for _, want := range e.placements.desired {
		found := false
		for _, site := range e.sites.desired {
			if site.path != want.floor {
				continue
			}
			geometry, err := plannedAPDesiredGeometry(site)
			if err != nil {
				return nil, err
			}
			if err := validatePlannedAPPosition(want.payload, geometry); err != nil {
				return nil, err
			}
			found = true
			break
		}
		if !found {
			return nil, errPlannedAPModel
		}
		if site, ok := existing[want.floor]; ok {
			if site.Type != "floor" {
				return nil, errInvalidResponse
			}
			available = append(available, want)
		} else if err := validatePlannedAPCreate(want); err != nil {
			return nil, err
		}
	}
	var placement *configurationWrite
	if len(available) > 0 {
		subset := *e.placements
		subset.desired = available
		placement, err = subset.nextWithGeometry(ctx, e.sites.desired)
		if err != nil {
			return nil, err
		}
	}
	var image *configurationWrite
	if e.images != nil {
		availableImages := []floorImageIntent{}
		additional := 0
		for _, want := range e.images.desired {
			recorded := false
			for _, receipt := range e.images.receipts {
				if receipt.Section == "sites" && receipt.Resource == want.resource() {
					recorded = true
				}
			}
			if !recorded {
				additional++
			}
			if site, ok := existing[want.floor]; ok {
				if site.Type != "floor" {
					return nil, errInvalidResponse
				}
				availableImages = append(availableImages, want)
			} else if recorded {
				return nil, errConfigurationStale
			}
		}
		if len(e.images.receipts)+additional > 256 {
			return nil, errConfigurationTransition
		}
		if len(availableImages) > 0 {
			subset := *e.images
			subset.desired = availableImages
			image, err = subset.next(ctx)
			if err != nil {
				return nil, err
			}
		}
		if mutation == nil && len(availableImages) != len(e.images.desired) {
			return nil, errInvalidResponse
		}
	}
	if mutation != nil {
		handoff := false
		if e.placements.comparison != nil && mutation.action == "Update" && mutation.desired.kind == "floor" {
			for _, record := range e.placements.identities {
				if record.keyID != "" && record.receipt.Operation.ParentID == mutation.id {
					if !e.floorHandoff {
						return nil, errConfigurationTransition
					}
					handoff = true
				}
			}
		}
		write, err := e.sites.nextObserved(ctx, observed)
		if err != nil {
			return nil, err
		}
		if handoff {
			write.resource, err = e.floorHandoffResource(ctx, observed, mutation, write.resource)
			if err != nil {
				return nil, err
			}
		}
		return e.guardRetainedPrivate(ctx, retained, write)
	}
	if len(available) != len(e.placements.desired) {
		return nil, errInvalidResponse
	}
	if image != nil {
		placement, err = e.guardPrivateDesign(ctx, observed, image)
	} else {
		placement, err = e.guardPrivateDesign(ctx, observed, placement)
	}
	if err != nil {
		return nil, err
	}
	return e.guardRetainedPrivate(ctx, retained, placement)
}
func (e *sitePlannedAPExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil {
		return false, errConfigurationTransition
	}
	if isFloorImageOperation(op.Section, op.Resource) {
		return false, errConfigurationTransition
	}
	if plannedAPResourceKey(op.Resource) != "" {
		return e.placements.verify(ctx, op)
	}
	return e.sites.verify(ctx, op)
}
