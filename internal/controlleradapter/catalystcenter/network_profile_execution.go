// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sort"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type switchingProfileObservation struct {
	desired, actual networkProfile
	binding         string
	native          *switchingProfileNativeObservation
}
type switchingProfileExecutor struct {
	client          *client
	desired         []switchingProfileIntent
	createCustody   bool
	creatorOwner    string
	creators        map[string]ciskov1.NetworkControllerConfigurationActionApplication
	receiptCount    int
	createAdmission func(context.Context, string, configurationResourceIdentity) error
}

func (e *switchingProfileExecutor) count() int { return len(e.desired) }

// Resolve every dependency before planning the first mutation. Bare names are
// accepted only when unique across projects, just like the upstream model.
func (e *switchingProfileExecutor) observe(ctx context.Context) ([]switchingProfileObservation, error) {
	profiles, err := e.client.listSwitchingProfiles(ctx)
	if err != nil {
		return nil, err
	}
	projects := map[string]templateProject{}
	definitions := map[string]templateSummary{}
	needsTemplates := false
	for _, w := range e.desired {
		needsTemplates = needsTemplates || len(w.dayn)+len(w.onboarding) > 0
	}
	if needsTemplates {
		projects, err = e.client.listTemplateProjects(ctx)
		if err != nil {
			return nil, err
		}
		definitions, err = e.client.listTemplateDefinitions(ctx)
		if err != nil {
			return nil, err
		}
	}
	ownedIDs := map[string]string{}
	for key, r := range e.creators {
		id, err := switchingCreatedTarget(r)
		expected, valid := switchingCreateKey(r.Operation.Resource)
		if err != nil || !valid || key != expected || r.Operation.ConfigUID != e.creatorOwner {
			return nil, errConfigurationOwned
		}
		if _, ok := ownedIDs[id]; ok {
			return nil, errConfigurationOwned
		}
		ownedIDs[id] = key
	}
	out := []switchingProfileObservation{}
	for _, w := range e.desired {
		desired, err := resolvedSwitchingProfile(w, projects, definitions)
		if err != nil {
			return nil, err
		}
		binding, err := boundSwitchingProfileDependency(w, desired, projects, definitions)
		if err != nil {
			return nil, err
		}
		actual := profiles[w.name]
		var native *switchingProfileNativeObservation
		if actual.ID != "" {
			native, err = e.client.readSwitchingProfileNative(ctx, actual)
			if err != nil {
				return nil, err
			}
			actual = native.profile
			if _, err = profileTemplateSets(actual); err != nil {
				return nil, err
			}
		}
		if key, ok := ownedIDs[actual.ID]; ok && key != switchingNameDigest(w.name) {
			return nil, errConfigurationOwned
		}
		if r, ok := e.creators[switchingNameDigest(w.name)]; ok {
			id, err := switchingCreatedTarget(r)
			if err != nil || r.Operation.ConfigUID != e.creatorOwner || actual.ID != id {
				return nil, errConfigurationOwned
			}
		}
		out = append(out, switchingProfileObservation{desired: desired, actual: actual, binding: binding, native: native})
	}
	return out, nil
}

// The API may omit empty groups and reorder memberships. Unknown groups are
// rejected before replacement rather than silently erasing unmanaged data.
func profileTemplateSets(p networkProfile) (map[string][]string, error) {
	out := map[string][]string{"cli.templates": {}, "day0.templates": {}}
	groups := map[string]bool{}
	for _, a := range p.Attributes {
		if groups[a.Key] {
			return nil, errInvalidResponse
		}
		groups[a.Key] = true
		if a.Key == "site.settings" {
			if a.raw == nil {
				return nil, errInvalidResponse
			}
			continue
		}
		if _, known := out[a.Key]; !known {
			return nil, errors.New("unsupported or repeated network profile attribute group")
		}
		seen := map[string]bool{}
		for _, v := range a.Attributes {
			if v.Key != "template.id" || !validAPIID(v.Value) || seen[v.Value] {
				return nil, errInvalidResponse
			}
			seen[v.Value] = true
			out[a.Key] = append(out[a.Key], v.Value)
		}
		sort.Strings(out[a.Key])
	}
	return out, nil
}
func switchingProfileMatches(w, g networkProfile) bool {
	a, err := profileTemplateSets(w)
	if err != nil {
		return false
	}
	b, err := profileTemplateSets(g)
	if err != nil {
		return false
	}
	return w.Name == g.Name && w.Namespace == g.Namespace && reflect.DeepEqual(a, b)
}
func profileDependencyIdentity(p networkProfile) string {
	sets, _ := profileTemplateSets(p)
	raw, _ := json.Marshal(sets)
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}
func (e *switchingProfileExecutor) next(ctx context.Context) (*configurationWrite, error) {
	frozen := &switchingProfileExecutor{client: e.client, desired: make([]switchingProfileIntent, len(e.desired)), createCustody: e.createCustody, creatorOwner: e.creatorOwner, creators: e.creators, receiptCount: e.receiptCount, createAdmission: e.createAdmission}
	for i, want := range e.desired {
		frozen.desired[i] = switchingProfileIntent{name: want.name, dayn: append([]string(nil), want.dayn...), onboarding: append([]string(nil), want.onboarding...), sites: append([]string(nil), want.sites...)}
	}
	observed, err := frozen.observe(ctx)
	if err != nil {
		return nil, err
	}
	if frozen.createCustody {
		missing := 0
		for _, o := range observed {
			if o.actual.ID == "" {
				missing++
			}
		}
		if missing > 256-frozen.receiptCount {
			return nil, errConfigurationBusy
		}
	}
	for _, o := range observed {
		if o.actual.ID != "" && switchingProfileMatches(o.desired, o.actual) {
			continue
		}
		method, action, path := http.MethodPost, "Create", networkProfilesPath
		if o.actual.ID != "" {
			method, action, path = http.MethodPut, "Update", networkProfilesPath+"/"+o.actual.ID
		}
		payload := o.desired
		payload.Attributes = append([]networkProfileAttribute(nil), payload.Attributes...)
		for _, a := range o.actual.Attributes {
			if a.Key == "site.settings" {
				payload.Attributes = append(payload.Attributes, a)
			}
		}
		body, _ := json.Marshal(payload)
		resource := o.desired.Name
		if action == "Create" && frozen.createCustody {
			resource = switchingCreateLocal + switchingNameDigest(o.desired.Name)
		}
		write := &configurationWrite{resource: resource, action: action, identity: configurationResourceIdentity{target: o.actual.ID, parent: o.binding}, submit: func(ctx context.Context) (Task, error) {
			raw, err := frozen.client.mutate(ctx, method, path, body)
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
		// Reuse the one-use pre-submit capability: only these repeated native
		// observations can prove the original mutation was never invoked.
		// Transport/receipt failures below the guard cannot acquire that proof.
		guardConfigurationPreflight(write, func(ctx context.Context) error {
			for i := range 2 {
				if i == 1 && action == "Create" && frozen.createCustody {
					if frozen.createAdmission == nil {
						return errConfigurationTransition
					}
					if err := frozen.createAdmission(ctx, resource, write.identity); err != nil {
						return err
					}
				}
				fresh, err := frozen.observe(ctx)
				if err != nil {
					return err
				}
				if !sameSwitchingProfileObservation(observed, fresh) {
					return errConfigurationStale
				}
			}
			return nil
		})
		return write, nil
	}
	return nil, nil
}
func (e *switchingProfileExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || (op.Action != "Create" && op.Action != "Update") || !validAPIID(op.ParentID) || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return false, errConfigurationTransition
	}
	if !validSwitchingProfileBinding(op.ParentID) && !legacyEmptySwitchingProfileBinding(op.ParentID) {
		return false, errSwitchingProfileBinding
	}
	matchedID := ""
	for range 2 {
		found := false
		observed, err := e.observe(ctx)
		if err != nil {
			return false, err
		}
		for _, o := range observed {
			if o.desired.Name != op.Resource {
				continue
			}
			found = true
			if o.binding != op.ParentID && !(legacyEmptySwitchingProfileBinding(op.ParentID) && profileDependencyIdentity(o.desired) == op.ParentID) {
				return false, errSwitchingProfileBinding
			}
			if o.actual.ID == "" {
				return false, nil
			}
			if op.TargetID != "" && o.actual.ID != op.TargetID {
				return false, errors.New("network profile identity changed")
			}
			if matchedID != "" && matchedID != o.actual.ID {
				return false, errConfigurationStale
			}
			if !switchingProfileMatches(o.desired, o.actual) {
				return false, nil
			}
			matchedID = o.actual.ID
		}
		if !found || matchedID == "" {
			return false, errConfigurationTransition
		}
	}
	return true, nil
}
func (e *switchingProfileExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	observed, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	drift := []configapi.NetworkControllerDriftEntry{}
	for _, o := range observed {
		change := configapi.NetworkControllerDriftChangeUpdate
		if o.actual.ID == "" {
			change = configapi.NetworkControllerDriftChangeAdd
		} else if switchingProfileMatches(o.desired, o.actual) {
			continue
		}
		drift = append(drift, configapi.NetworkControllerDriftEntry{Section: "network_profiles", Resource: o.desired.Name, Path: "/switching", Change: change})
	}
	return drift, nil
}
