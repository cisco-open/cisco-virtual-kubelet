// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func profileSiteAssignmentsPath(id string) string {
	return "/dna/intent/api/v1/networkProfilesForSites/" + id + "/siteAssignments"
}
func (c *client) profileAssignedSites(ctx context.Context, id string) (map[string]bool, error) {
	if !validAPIID(id) {
		return nil, errInvalidResponse
	}
	type assignment struct {
		ID string `json:"id"`
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	items, err := listInventoryQueryWithGet(ctx, get, profileSiteAssignmentsPath(id), nil, func(a assignment) string { return a.ID })
	if err != nil {
		return nil, err
	}
	result := map[string]bool{}
	for _, a := range items {
		if !validAPIID(a.ID) {
			return nil, errInvalidResponse
		}
		result[a.ID] = true
	}
	return result, nil
}

type profileSiteObservation struct {
	resource, profileID, siteID string
	present                     bool
}
type profileSiteIntent struct {
	name  string
	sites []string
}

type profileSiteExecutor struct {
	applicationPlan  func(context.Context, []profileSiteObservation) (string, error)
	applicationProof func(context.Context, string) error
	wireless         bool
	client           *client
	desired          []profileSiteIntent
}

func (e *profileSiteExecutor) count() int {
	n := 0
	for _, p := range e.desired {
		n += len(p.sites)
	}
	return n
}
func (e *profileSiteExecutor) observe(ctx context.Context) ([]profileSiteObservation, error) {
	if e.count() == 0 {
		return nil, nil
	}
	profiles := map[string]string{}
	if e.wireless {
		rows, err := e.client.wirelessProfileInventory(ctx)
		if err != nil {
			return nil, err
		}
		for name, row := range rows {
			profiles[name] = row.id
		}
	} else {
		rows, err := e.client.listSwitchingProfiles(ctx)
		if err != nil {
			return nil, err
		}
		for name, row := range rows {
			profiles[name] = row.ID
		}
	}
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, s := range sites {
		if _, ok := byPath[s.NameHierarchy]; ok {
			return nil, errInvalidResponse
		}
		byPath[s.NameHierarchy] = s
	}
	out := []profileSiteObservation{}
	seen := map[string]bool{}
	for _, p := range e.desired {
		if len(p.sites) == 0 {
			continue
		}
		profile := profiles[p.name]
		assigned := map[string]bool{}
		if profile != "" {
			assigned, err = e.client.profileAssignedSites(ctx, profile)
			if err != nil {
				return nil, err
			}
		}
		for _, path := range p.sites {
			site, exists := byPath[path]
			if !exists || !validAPIID(site.ID) {
				return nil, errors.New("network profile site dependency is missing")
			}
			resource := tagMembershipIdentity(path, p.name)
			if seen[resource] {
				return nil, errConfigurationTransition
			}
			seen[resource] = true
			out = append(out, profileSiteObservation{resource: resource, profileID: profile, siteID: site.ID, present: assigned[site.ID]})
		}
	}
	return out, nil
}
func (e *profileSiteExecutor) next(ctx context.Context) (*configurationWrite, error) {
	frozen := &profileSiteExecutor{wireless: e.wireless, client: e.client, desired: make([]profileSiteIntent, len(e.desired)), applicationPlan: e.applicationPlan, applicationProof: e.applicationProof}
	for i, want := range e.desired {
		frozen.desired[i] = profileSiteIntent{name: want.name, sites: append([]string(nil), want.sites...)}
	}
	observed, err := frozen.observe(ctx)
	if err != nil {
		return nil, err
	}
	predecessor := ""
	if frozen.applicationPlan != nil {
		predecessor, err = frozen.applicationPlan(ctx, observed)
		if err != nil {
			return nil, err
		}
	}
	for _, o := range observed {
		// The preceding profile executor creates declared profiles first.
		if o.present || o.profileID == "" {
			continue
		}
		body, _ := json.Marshal(map[string]string{"id": o.siteID})
		resource := o.resource
		if frozen.applicationPlan != nil {
			if !profileSiteDigestValid(predecessor) {
				return nil, errProfileSiteApplications
			}
			resource = profileSiteAddPrefix + profileSiteDigest(o.resource) + ":" + predecessor
		}
		write := &configurationWrite{resource: resource, action: "Update", identity: configurationResourceIdentity{target: o.siteID, parent: o.profileID}, submit: func(ctx context.Context) (Task, error) {
			raw, err := frozen.client.mutate(ctx, http.MethodPost, profileSiteAssignmentsPath(o.profileID), body)
			if err != nil {
				return Task{}, err
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				return Task{}, errInvalidResponse
			}
			return Task{ID: receipt.Response.ID}, nil
		}}
		guardConfigurationPreflight(write, func(ctx context.Context) error {
			for range 2 {
				fresh, err := frozen.observe(ctx)
				if err != nil {
					return err
				}
				if !reflect.DeepEqual(observed, fresh) {
					return errConfigurationStale
				}
			}
			if frozen.applicationProof != nil {
				return frozen.applicationProof(ctx, predecessor)
			}
			return nil
		})
		return write, nil
	}
	return nil, nil
}
func (e *profileSiteExecutor) selectedPair(op *ciskov1.NetworkControllerConfigurationOperation) (*profileSiteExecutor, error) {
	if op == nil || op.TargetSerial != "" || len(op.CredentialVersions) > 0 || op.Action != "Update" || !validAPIID(op.TargetID) || !validAPIID(op.ParentID) {
		return nil, errConfigurationTransition
	}
	// Reconstruct only the acknowledged pair from source and persisted IDs.
	// Future source associations must not multiply recovery membership reads.
	var selected *profileSiteExecutor
	for _, want := range e.desired {
		for _, path := range want.sites {
			if profileSiteResourceMatches(op.Resource, tagMembershipIdentity(path, want.name)) {
				if selected != nil {
					return nil, errConfigurationTransition
				}
				selected = &profileSiteExecutor{wireless: e.wireless, client: e.client, desired: []profileSiteIntent{{name: want.name, sites: []string{path}}}}
			}
		}
	}
	if selected == nil {
		return nil, errConfigurationTransition
	}
	return selected, nil
}

func profileSiteObservationMatches(observed []profileSiteObservation, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if len(observed) != 1 {
		return false, errConfigurationTransition
	}
	o := observed[0]
	if o.profileID != op.ParentID || o.siteID != op.TargetID || !profileSiteResourceMatches(op.Resource, o.resource) {
		return false, errors.New("network profile assignment identity changed")
	}
	return o.present, nil
}

func (e *profileSiteExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	selected, err := e.selectedPair(op)
	if err != nil {
		return false, err
	}
	for range 2 {
		observed, err := selected.observe(ctx)
		if err != nil {
			return false, err
		}
		if done, err := profileSiteObservationMatches(observed, op); err != nil || !done {
			return false, err
		}
	}
	return true, nil
}
func (e *profileSiteExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	observed, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	drift := []configapi.NetworkControllerDriftEntry{}
	for _, o := range observed {
		if !o.present {
			resource := o.resource
			if e.wireless {
				resource = "wireless/" + resource
			}
			drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "network_profiles", Resource: resource, Path: "/site_assignments", Change: configapi.NetworkControllerDriftChangeAdd})
		}
	}
	return drift, nil
}
