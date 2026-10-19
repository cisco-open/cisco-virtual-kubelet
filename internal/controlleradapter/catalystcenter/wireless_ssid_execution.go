// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync/atomic"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type wirelessSSIDExecutor struct {
	client   *client
	desired  []wirelessSSIDIntent
	secrets  *configurationSecretSnapshot
	owner    string
	receipts []ciskov1.NetworkControllerConfigurationCredentialApplication
}

func (e *wirelessSSIDExecutor) count() int { return len(e.desired) }
func (w wirelessSSIDIntent) resource(site string) string {
	prefix := "ssid:"
	if len(w.credentials) > 0 {
		prefix = "ssid-credential:"
	}
	return prefix + site + ":" + w.name
}
func (e *wirelessSSIDExecutor) global(ctx context.Context) (string, error) {
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return "", err
	}
	for _, site := range sites {
		if site.NameHierarchy == "Global" && site.Type == "global" && validAPIID(site.ID) {
			return site.ID, nil
		}
	}
	return "", errInvalidResponse
}

func wirelessSSIDMatches(want, got map[string]any) bool {
	w := cloneWirelessSSIDFields(want)
	delete(w, "multiPSKSettings")
	if !wirelessAPPublicMatches(w, got) {
		return false
	}
	entries, owned := want["multiPSKSettings"].([]map[string]any)
	if !owned {
		return true
	}
	// The appliance may omit an empty optional collection. This readback
	// equivalence does not authorize removal: credential history is checked
	// independently before planning any write.
	if _, present := got["multiPSKSettings"]; !present && len(entries) == 0 {
		return true
	}
	actual, ok := got["multiPSKSettings"].([]map[string]any)
	if !ok || len(entries) != len(actual) {
		return false
	}
	for _, entry := range entries {
		found := false
		for _, other := range actual {
			if entry["priority"] == other["priority"] && wirelessAPPublicMatches(entry, other) {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func (e *wirelessSSIDExecutor) next(ctx context.Context) (*configurationWrite, error) {
	return e.plan(ctx, nil)
}

func (e *wirelessSSIDExecutor) plan(ctx context.Context, plannedACL map[string]bool) (*configurationWrite, error) {
	return e.planObserved(ctx, plannedACL, nil)
}

// Reuse the complete planner for submission preflight, including ACL and OPEN
// companion dependencies. The observer binds the selected public readback, not
// unrelated inventory rows or API-returned write-only credential values.
func (e *wirelessSSIDExecutor) planObserved(ctx context.Context, plannedACL map[string]bool, observe func(string, map[string]wirelessSSIDMetadata) error) (*configurationWrite, error) {
	site, err := e.global(ctx)
	if err != nil {
		return nil, err
	}
	actual, err := e.client.wirelessSSIDInventory(ctx, site)
	if err != nil {
		return nil, err
	}
	if observe != nil {
		if err := observe(site, actual); err != nil {
			return nil, err
		}
	}
	wants := map[string]wirelessSSIDIntent{}
	for _, w := range e.desired {
		wants[w.name] = w
	}
	var acls map[string]wirelessACL
	ordered := append([]wirelessSSIDIntent(nil), e.desired...)
	// OPEN companions must exist before an OPEN-SECURED SSID references them.
	sort.SliceStable(ordered, func(i, j int) bool {
		return ordered[i].metadata["authType"] != "OPEN-SECURED" && ordered[j].metadata["authType"] == "OPEN-SECURED"
	})
	var first *configurationWrite
	for _, want := range ordered {
		got, exists := actual[want.name]
		if want.metadata["authType"] != "OPEN" {
			for name, other := range actual {
				if name != want.name && other.fields["openSsid"] == want.name {
					// Detach the companion reference and verify that separate
					// operation before changing its authentication mode.
					return nil, errConfigurationOwned
				}
			}
		}
		resource := want.resource(site)
		var prior *ciskov1.NetworkControllerConfigurationCredentialApplication
		for i := range e.receipts {
			r := &e.receipts[i]
			parts := strings.SplitN(r.Resource, ":", 3)
			if r.Section != "wireless" || len(parts) != 3 || (parts[0] != "ssid" && parts[0] != "ssid-credential" && !strings.HasPrefix(parts[0], ssidHistoryResourcePrefix)) || parts[2] != want.name {
				continue
			}
			if prior != nil || r.ConfigUID != e.owner || !configurationCredentialResourceSame("wireless", r.Resource, resource) || !validAPIID(r.TargetID) || !configurationOperationID.MatchString(r.OperationID) || len(r.Versions) == 0 {
				return nil, errConfigurationOwned
			}
			prior = r
		}
		if prior != nil && len(want.credentials) == 0 {
			return nil, errConfigurationSecret
		}
		if exists && len(want.credentials) > 0 && (prior == nil || prior.TargetID != got.id) {
			return nil, errConfigurationOwned
		}
		if exists && len(want.credentials) == 0 && wirelessSSIDMatches(want.metadata, got.fields) {
			continue
		}
		if exists && (got.sensor || got.inheritedSiteID != "" && got.inheritedSiteID != site) {
			return nil, errConfigurationOwned
		}
		var observed *wirelessSSIDMetadata
		if exists {
			observed = &got
		}
		body, versions, err := want.payload(e.secrets, e.owner, observed)
		if err != nil {
			return nil, err
		}
		_, ownsSet := want.metadata["multiPSKSettings"]
		rotate, history, err := wirelessSSIDCredentialTransition(prior, versions, ownsSet)
		if err != nil {
			return nil, err
		}
		if len(history) > 0 {
			resource = ssidHistoryPrefix(history) + ":" + site + ":" + want.name
		}
		if err := validateSSIDCredentialHistory("wireless", resource, versions, history); err != nil {
			return nil, err
		}
		if exists && !rotate && wirelessSSIDMatches(want.metadata, got.fields) {
			continue
		}
		effective := mergeWirelessAPFields(got.fields, want.metadata)
		blocked := false
		if acl, _ := effective["aclName"].(string); acl != "" {
			if acls == nil {
				acls, err = e.client.wirelessACLs(ctx)
				if err != nil {
					return nil, err
				}
			}
			if _, ok := acls[acl]; !ok {
				if !plannedACL[acl] {
					return nil, errWirelessSSIDModel
				}
				blocked = true
			}
		}
		if effective["authType"] == "OPEN-SECURED" {
			companion, _ := effective["openSsid"].(string)
			if companion == "" || companion == want.name {
				return nil, errWirelessSSIDModel
			}
			other, ok := actual[companion]
			if !ok {
				planned, known := wants[companion]
				if !known || planned.metadata["authType"] != "OPEN" {
					return nil, errWirelessSSIDModel
				}
				blocked = true
			} else if other.fields["authType"] != "OPEN" || other.sensor || other.inheritedSiteID != "" && other.inheritedSiteID != site {
				return nil, errWirelessSSIDModel
			}
			if planned, known := wants[companion]; known && planned.metadata["authType"] != "OPEN" {
				return nil, errWirelessSSIDModel
			}
			for name, other := range actual {
				if name != want.name && other.fields["openSsid"] == companion {
					return nil, errConfigurationOwned
				}
			}
			for name, other := range wants {
				if name != want.name && other.metadata["openSsid"] == companion {
					return nil, errWirelessSSIDModel
				}
			}
		}
		if blocked {
			continue
		}
		method, path, action := http.MethodPost, wirelessSSIDPath(site), "Create"
		identity := configurationResourceIdentity{parent: site, credentials: versions, ssidCredentialHistory: history}
		if exists {
			method, path, action = http.MethodPut, path+"/"+url.PathEscape(got.id), "Update"
			identity.target = got.id
		}
		plannedObservation := got
		plannedObservation.fields = cloneWirelessSSIDFields(got.fields)
		var used atomic.Bool
		write := &configurationWrite{resource: resource, action: action, identity: identity, submit: func(ctx context.Context) (Task, error) {
			// Consume even on preflight or ambiguous submission failure. Durable journal
			// recovery, rather than a retained closure, decides what may happen next.
			if !used.CompareAndSwap(false, true) {
				return Task{}, errConfigurationTransition
			}
			fresh, err := e.planObserved(ctx, nil, func(currentSite string, inventory map[string]wirelessSSIDMetadata) error {
				current, present := inventory[want.name]
				if currentSite != site || present != exists || present && !reflect.DeepEqual(current, plannedObservation) {
					return errConfigurationStale
				}
				return nil
			})
			if err != nil {
				return Task{}, err
			}
			if fresh == nil || fresh.resource != resource || fresh.action != action || !reflect.DeepEqual(fresh.identity, identity) {
				return Task{}, errConfigurationStale
			}
			raw, err := e.client.mutate(ctx, method, path, body)
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
		if first == nil {
			first = write
		}
	}
	return first, nil
}

func (e *wirelessSSIDExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	_, ok, err := e.verifySSID(ctx, op, false)
	return ok, err
}
func (e *wirelessSSIDExecutor) verifyCredential(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (string, bool, error) {
	return e.verifySSID(ctx, op, true)
}
func (e *wirelessSSIDExecutor) verifySSID(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation, credential bool) (string, bool, error) {
	if op == nil || !validAPIID(op.ParentID) || op.TargetSerial != "" || (op.Action != "Create" && op.Action != "Update") || (op.Action == "Create" && op.TargetID != "") || (op.Action == "Update" && !validAPIID(op.TargetID)) {
		return "", false, errConfigurationTransition
	}
	site, err := e.global(ctx)
	if err != nil {
		return "", false, err
	}
	if op.ParentID != site {
		return "", false, errConfigurationStale
	}
	for _, want := range e.desired {
		if !configurationCredentialResourceSame("wireless", want.resource(site), op.Resource) {
			continue
		}
		if credential {
			if op.ConfigUID != e.owner || op.Section != "wireless" || len(want.credentials) == 0 || len(op.CredentialVersions) == 0 {
				return "", false, errConfigurationSecret
			}
			_, versions, err := resolveVersionedConfigurationCredentials(e.secrets, e.owner, "wireless", want.credentials)
			if err != nil || !slices.Equal(versions, op.CredentialVersions) {
				return "", false, errConfigurationSecret
			}
			if err := validateSSIDCredentialHistory(op.Section, op.Resource, versions, op.SSIDCredentialHistory); err != nil {
				return "", false, err
			}
			var prior *ciskov1.NetworkControllerConfigurationCredentialApplication
			for i := range e.receipts {
				r := &e.receipts[i]
				if r.Section == "wireless" && configurationCredentialResourceSame("wireless", r.Resource, op.Resource) {
					if prior != nil || r.ConfigUID != e.owner || (op.TargetID != "" && r.TargetID != op.TargetID) {
						return "", false, errConfigurationOwned
					}
					prior = r
				}
			}
			_, ownsSet := want.metadata["multiPSKSettings"]
			_, history, err := wirelessSSIDCredentialTransition(prior, versions, ownsSet)
			if err != nil || !slices.Equal(history, op.SSIDCredentialHistory) {
				return "", false, errConfigurationSecret
			}
		} else if len(want.credentials) > 0 || len(op.CredentialVersions) > 0 || len(op.SSIDCredentialHistory) > 0 {
			return "", false, errConfigurationSecret
		}
		actual, err := e.client.wirelessSSIDInventory(ctx, site)
		if err != nil {
			return "", false, err
		}
		got, exists := actual[want.name]
		if !exists {
			return "", false, nil
		}
		if got.sensor || got.inheritedSiteID != "" && got.inheritedSiteID != site || (op.TargetID != "" && op.TargetID != got.id) {
			return "", false, errConfigurationStale
		}
		return got.id, wirelessSSIDMatches(want.metadata, got.fields), nil
	}
	return "", false, errConfigurationTransition
}

func (e *wirelessSSIDExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	for _, want := range e.desired {
		if len(want.credentials) > 0 {
			return nil, errConfigurationSecret
		}
	}
	site, err := e.global(ctx)
	if err != nil {
		return nil, err
	}
	actual, err := e.client.wirelessSSIDInventory(ctx, site)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, want := range e.desired {
		got, exists := actual[want.name]
		if exists && wirelessSSIDMatches(want.metadata, got.fields) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if exists {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "wireless", Resource: want.resource(site), Path: "/ssids", Change: change})
	}
	return out, nil
}
