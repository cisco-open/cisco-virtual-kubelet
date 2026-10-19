// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const fabricZonesPath = "/dna/intent/api/v1/sda/fabricZones"

type fabricZone struct {
	ID             string `json:"id,omitempty"`
	SiteID         string `json:"siteId"`
	Authentication string `json:"authenticationProfileName"`
}

func (z fabricZoneIntent) resource() string {
	return fmt.Sprintf("fabric-zone:%x", sha256.Sum256([]byte(z.path)))
}
func (c *client) fabricZones(ctx context.Context) (map[string]fabricZone, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryPagesWithGet(ctx, get, fabricZonesPath, nil, func(z fabricZone) string { return z.ID }, 50)
	if err != nil {
		return nil, err
	}
	out := map[string]fabricZone{}
	for _, zone := range rows {
		if !validAPIID(zone.ID) || !validAPIID(zone.SiteID) || !validFabricAuthentication(zone.Authentication) {
			return nil, errInvalidResponse
		}
		if _, exists := out[zone.SiteID]; exists {
			return nil, errInvalidResponse
		}
		out[zone.SiteID] = zone
	}
	return out, nil
}

type fabricZoneExecutor struct {
	client  *client
	desired []fabricZoneIntent
}
type fabricZoneState struct {
	want             fabricZoneIntent
	siteID, parentID string
	actual           fabricZone
	exists           bool
}

func (o fabricZoneState) operationResource() string {
	return o.want.resource() + fmt.Sprintf(":%x", sha256.Sum256([]byte(o.parentID)))
}
func (e *fabricZoneExecutor) count() int { return len(e.desired) }
func (e *fabricZoneExecutor) observe(ctx context.Context) ([]fabricZoneState, error) {
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	byID := map[string]Site{}
	for _, site := range sites {
		byPath[site.NameHierarchy] = site
		byID[site.ID] = site
	}
	fabrics, err := e.client.fabricSites(ctx)
	if err != nil {
		return nil, err
	}
	zones, err := e.client.fabricZones(ctx)
	if err != nil {
		return nil, err
	}
	for id := range fabrics {
		if _, ok := byID[id]; !ok {
			return nil, errInvalidResponse
		}
	}
	for id := range zones {
		if _, ok := byID[id]; !ok {
			return nil, errInvalidResponse
		}
	}
	out := []fabricZoneState{}
	for _, want := range e.desired {
		site, exists := byPath[want.path]
		parent, hasParent := byPath[want.parent]
		if !exists || !hasParent || site.Type == "global" {
			return nil, errInvalidResponse
		}
		if _, collision := fabrics[site.ID]; collision {
			return nil, errConfigurationStale
		}
		actual, exists := zones[site.ID]
		fabric, ready := fabrics[parent.ID]
		nearest := ""
		for id := range fabrics {
			path := byID[id].NameHierarchy
			if strings.HasPrefix(want.path, path+"/") && len(path) > len(nearest) {
				nearest = path
			}
		}
		for id := range zones {
			if strings.HasPrefix(want.path, byID[id].NameHierarchy+"/") {
				return nil, errConfigurationStale
			}
		}
		if ready && nearest != want.parent || !ready && exists {
			return nil, errConfigurationStale
		}
		out = append(out, fabricZoneState{want: want, siteID: site.ID, parentID: fabric.ID, actual: actual, exists: exists})
	}
	return out, nil
}
func (e *fabricZoneExecutor) next(ctx context.Context) (*configurationWrite, error) {
	states, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	var first *configurationWrite
	for _, state := range states {
		if state.exists {
			if state.actual.Authentication != state.want.authentication {
				return nil, errConfigurationTransition
			}
			continue
		}
		if state.parentID == "" {
			return nil, errConfigurationBusy
		}
		if first != nil {
			continue
		}
		body := fabricZone{SiteID: state.siteID, Authentication: state.want.authentication}
		first = &configurationWrite{resource: state.operationResource(), action: "Create", identity: configurationResourceIdentity{parent: state.siteID}, submit: func(ctx context.Context) (Task, error) {
			return e.client.submitTask(ctx, fabricZonesPath, []fabricZone{body})
		}}
	}
	return first, nil
}
func (e *fabricZoneExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Create" || op.TargetID != "" || op.TargetSerial != "" || !validAPIID(op.ParentID) || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	states, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for _, state := range states {
		if state.parentID != "" && state.operationResource() == op.Resource {
			if state.siteID != op.ParentID {
				return false, errConfigurationStale
			}
			return state.exists && state.actual.Authentication == state.want.authentication, nil
		}
	}
	return false, errConfigurationStale
}
func (e *fabricZoneExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	states, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, state := range states {
		if state.exists && state.actual.Authentication == state.want.authentication {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if state.exists {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: state.want.resource(), Path: "/fabric_sites/fabric_zones", Change: change})
	}
	return out, nil
}
