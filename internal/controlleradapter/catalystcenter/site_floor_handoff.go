// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const siteFloorHandoffAnnotation = "cisco.vk/catalyst-center-floor-ap-handoff"

// Complete floor-local membership is mandatory: a floor write must not rescale
// an unowned AP as an untracked side effect. Raw state remains in memory.
func (e *plannedAPExecutor) floorEffectSet(ctx context.Context, id string, future *Site, completion bool) ([]string, error) {
	if e.comparison == nil || e.comparison.check == nil {
		return nil, errConfigurationSecret
	}
	if err := e.comparison.check(ctx); err != nil {
		return nil, err
	}
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	var floor Site
	for _, site := range sites {
		if site.ID == id && site.Type == "floor" {
			floor = site
		}
	}
	if floor.ID == "" {
		return nil, errConfigurationOwned
	}
	rows, err := e.client.plannedAPNativePositions(ctx, id)
	if err != nil {
		return nil, err
	}
	byID := map[string]plannedAPNativePosition{}
	for _, row := range rows {
		byID[row.actual.ID] = row
	}
	effects := []string{}
	for key, anchor := range e.identities {
		if anchor.receipt.Operation.ParentID != id {
			continue
		}
		if anchor.keyID != e.comparison.keyID || anchor.receipt.Operation.ConfigUID != e.identityOwner {
			return nil, errConfigurationOwned
		}
		row, ok := byID[anchor.id]
		want := plannedAPIntent{floor: floor.NameHierarchy, payload: map[string]any{"name": row.actual.Name}}
		if !ok || plannedAPResource(want) != key {
			return nil, errConfigurationOwned
		}
		observed := plannedAPState{want: want, floorID: id, floor: floor, actual: row.actual, native: row.native}
		if !completion {
			if err := e.checkPrivateBaseline(ctx, observed); err != nil {
				return nil, err
			}
		}
		if future != nil {
			observed, err = plannedAPFloorEffect(observed, *future)
			if err != nil {
				return nil, err
			}
		} else {
			geometry, err := plannedAPObservedGeometry(floor)
			if err != nil {
				return nil, err
			}
			if err := validatePlannedAPPosition(map[string]any{"position": map[string]any{"x": *observed.actual.Position.X, "y": *observed.actual.Position.Y, "z": *observed.actual.Position.Z}}, geometry); err != nil {
				return nil, err
			}
		}
		view, err := plannedAPBaselineView(observed)
		if err != nil {
			return nil, err
		}
		mac, err := plannedAPNativeMAC(e.comparison, anchor.receipt.Operation.Resource, view)
		if err != nil {
			return nil, err
		}
		_, prior, _, err := plannedAPCurrentAuthority(key, anchor, e.applied, e.floorEffects)
		if err != nil {
			return nil, err
		}
		effects = append(effects, plannedAPFloorEffectPrefix+strings.TrimPrefix(key, "planned-ap:")+":"+anchor.id+":"+anchor.keyID+":"+mac+":"+prior)
	}
	if len(effects) != len(rows) {
		return nil, errConfigurationOwned
	}
	if _, err := plannedAPFloorEffectRoot(effects); err != nil {
		return nil, err
	}
	sort.Strings(effects)
	if err := e.comparison.check(ctx); err != nil {
		return nil, err
	}
	return effects, nil
}

func siteFloorFuture(actual []Site, mutation *siteMutation) (Site, error) {
	if mutation == nil || mutation.action != "Update" || mutation.desired.kind != "floor" {
		return Site{}, errConfigurationTransition
	}
	var future Site
	// Decode into a fresh value so existing floor pointers cannot be mutated.
	for _, site := range actual {
		if site.ID == mutation.id {
			raw, _ := json.Marshal(site)
			if json.Unmarshal(raw, &future) != nil {
				return Site{}, errInvalidResponse
			}
		}
	}
	if future.ID == "" || json.Unmarshal(mutation.body, &future) != nil {
		return Site{}, errConfigurationOwned
	}
	return siteFloorObservable(future)
}

// Qualify the expected effect against the lab's Site Design readback contract:
// dimensions are returned in feet as binary32 shortest decimals, even when the
// write uses metres. This changes only prediction, never the requested payload
// or native baseline comparisons. Other versions still require exact readback.
func siteFloorObservable(site Site) (Site, error) {
	geometry, err := plannedAPObservedGeometry(site)
	if err != nil {
		return Site{}, err
	}
	if *site.UnitsOfMeasure == "meters" {
		// Site Design multiplies binary32 request operands by its binary32
		// metres-to-feet factor. Converting in binary64 first changes
		// some unchanged axes and incorrectly predicts AP movement.
		geometry.width = float64(float32(*site.Width) * float32(1/0.3048))
		geometry.length = float64(float32(*site.Length) * float32(1/0.3048))
		geometry.height = float64(float32(*site.Height) * float32(1/0.3048))
	}
	w, err := plannedAPFloorCoordinate(geometry.width)
	if err != nil {
		return Site{}, err
	}
	l, err := plannedAPFloorCoordinate(geometry.length)
	if err != nil {
		return Site{}, err
	}
	h, err := plannedAPFloorCoordinate(geometry.height)
	if err != nil {
		return Site{}, err
	}
	u := "feet"
	site.Width, site.Length, site.Height, site.UnitsOfMeasure = &w, &l, &h, &u
	return site, nil
}

func (e *sitePlannedAPExecutor) floorHandoffResource(ctx context.Context, actual []Site, mutation *siteMutation, resource string) (string, error) {
	future, err := siteFloorFuture(actual, mutation)
	if err != nil {
		return "", err
	}
	effects, err := e.placements.floorEffectSet(ctx, mutation.id, &future, false)
	if err != nil {
		return "", err
	}
	root, err := plannedAPFloorEffectRoot(effects)
	if err != nil {
		return "", err
	}
	base := strings.TrimPrefix(resource, sitePrivateUpdatePrefix)
	if !strings.HasPrefix(resource, sitePrivateUpdatePrefix) || len(strings.Split(base, ":")) != 5 {
		return "", errConfigurationTransition
	}
	return sitePrivateFloorUpdatePrefix + base + ":" + root, nil
}

func (e *sitePlannedAPExecutor) checkFloorHandoff(ctx context.Context, resource string, identity configurationResourceIdentity) error {
	parts, ok := sitePrivateFloorUpdateParts(resource)
	if !ok {
		return errConfigurationTransition
	}
	actual, err := e.sites.client.ListSites(ctx)
	if err != nil {
		return err
	}
	mutation, err := nextSiteMutation(e.sites.desired, actual)
	if err != nil {
		return err
	}
	if mutation == nil || mutation.id != identity.target || mutation.parentID != identity.parent || sitePathDigest(mutation.desired.path) != parts[1] {
		return errConfigurationStale
	}
	base := sitePrivateUpdatePrefix + strings.Join(parts[:5], ":")
	want, err := e.floorHandoffResource(ctx, actual, mutation, base)
	if err != nil {
		return err
	}
	if want != resource {
		return errConfigurationStale
	}
	return nil
}

func (e *sitePlannedAPExecutor) configureFloorHandoff(allow func(context.Context) error) {
	prior := e.sites.submissionGuard
	e.sites.submissionGuard = func(ctx context.Context, resource, action string, identity configurationResourceIdentity) error {
		if _, ok := sitePrivateFloorUpdateParts(resource); !ok {
			return prior(ctx, resource, action, identity)
		}
		if err := allow(ctx); err != nil {
			return err
		}
		if err := e.checkFloorHandoff(ctx, resource, identity); err != nil {
			return err
		}
		if err := prior(ctx, resource, action, identity); err != nil {
			return err
		}
		if err := e.retainedGuard(ctx, resource, action, identity); err != nil {
			return err
		}
		return e.checkFloorHandoff(ctx, resource, identity)
	}
	e.sites.effectRecordsMatch = e.placements.identityRecordsMatch
	e.sites.verifyEffects = func(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) ([]string, error) {
		if err := allow(ctx); err != nil {
			return nil, err
		}
		parts, ok := sitePrivateFloorUpdateParts(op.Resource)
		if !ok {
			return nil, errConfigurationTransition
		}
		effects, err := e.placements.floorEffectSet(ctx, op.TargetID, nil, true)
		if err != nil {
			return nil, err
		}
		root, err := plannedAPFloorEffectRoot(effects)
		if err != nil || root != parts[5] {
			return nil, errConfigurationStale
		}
		if err := allow(ctx); err != nil {
			return nil, err
		}
		return effects, nil
	}
}
