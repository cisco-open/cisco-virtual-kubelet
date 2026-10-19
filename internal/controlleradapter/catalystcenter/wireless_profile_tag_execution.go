// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"reflect"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

type wirelessProfileTagExecutor struct {
	comparison *templateInputComparison
	removal    func(context.Context, wirelessProfileTagObservation) error
	client     *client
	desired    []wirelessProfileIntent
	policy     bool
}
type wirelessProfileTagObservation struct {
	resource, profileID, tagID string
	payload                    map[string]any
	actual                     *wirelessProfileTagMetadata
	baseline                   any
}

func (e *wirelessProfileTagExecutor) tags(p wirelessProfileIntent) []wirelessProfileTag {
	if e.policy {
		return p.policyTags
	}
	return p.siteTags
}
func (e *wirelessProfileTagExecutor) count() int {
	n := 0
	for _, p := range e.desired {
		for _, tag := range e.tags(p) {
			if len(tag.sites) > 0 {
				n++
			}
		}
	}
	return n
}
func wirelessTagDigest(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}
func wirelessTagResource(profile wirelessProfileMetadata, want wirelessProfileTag, refs wirelessProfileTagReferences, payload map[string]any, policy bool) (string, error) {
	identity, err := wirelessTagDigest([]string{profile.name, want.name})
	if err != nil {
		return "", err
	}
	bindings := map[string]any{"profile": profile.id}
	for _, path := range want.sites {
		bindings["site:"+path] = refs.sites[path]
	}
	if policy {
		names, _ := payload["apZones"].([]string)
		zones, _ := profile.fields["apZones"].([]map[string]any)
		for _, name := range names {
			for _, zone := range zones {
				if zone["apZoneName"] == name {
					bindings["zone:"+name] = zone
				}
			}
		}
	} else {
		if name, _ := payload["apProfileName"].(string); name != "" {
			bindings["ap:"+name] = refs.apProfiles[name]
		}
		if name, _ := payload["flexProfileName"].(string); name != "" {
			bindings["flexName"] = name
			if name != "default-flex-profile" {
				bindings["flexID"] = refs.flexProfiles[name]
			}
		}
	}
	fingerprint, err := wirelessTagDigest(bindings)
	if err != nil {
		return "", err
	}
	kind := "site-tag:"
	if policy {
		kind = "policy-tag:"
	}
	return kind + identity + ":" + fingerprint, nil
}
func (e *wirelessProfileTagExecutor) observe(ctx context.Context) ([]wirelessProfileTagObservation, error) {
	return e.observeDependencies(ctx, false)
}

func (e *wirelessProfileTagExecutor) observeDependencies(ctx context.Context, prospective bool) ([]wirelessProfileTagObservation, error) {
	if e.count() == 0 {
		return nil, nil
	}
	profiles, err := e.client.wirelessProfileInventory(ctx)
	if err != nil {
		return nil, err
	}
	out := []wirelessProfileTagObservation{}
	for _, p := range e.desired {
		var inventory map[string]wirelessProfileTagMetadata
		planned := map[string]map[string]any{}
		for _, tag := range e.tags(p) {
			// The pinned module emits no tag resource without any target sites.
			if len(tag.sites) == 0 {
				continue
			}
			profile, exists := profiles[p.name]
			if !exists && !prospective {
				return nil, errWirelessProfileModel
			}
			if prospective {
				var prior *wirelessProfileMetadata
				if exists {
					prior = &profile
				}
				dependencies, err := e.client.wirelessProfileReferences(ctx, p, prior)
				if err != nil {
					return nil, err
				}
				fields, err := p.payload(dependencies, prior)
				if err != nil {
					return nil, err
				}
				profile.name, profile.fields = p.name, fields
			}
			if inventory == nil && exists {
				inventory, err = e.client.wirelessProfileTagInventory(ctx, profile.id, e.policy)
				if err != nil {
					return nil, err
				}
			}
			got, exists := inventory[tag.name]
			var actual *wirelessProfileTagMetadata
			if exists {
				actual = &got
			}
			refs, err := e.client.collectWirelessProfileTagReferences(ctx, profile, tag, actual, e.policy, prospective)
			if err != nil {
				return nil, err
			}
			if prospective {
				for _, path := range p.sites {
					id := refs.sites[path]
					if !validAPIID(id) || path == "Global" {
						return nil, errWirelessProfileModel
					}
					refs.assigned[id] = true
				}
			}
			payload, err := tag.dependencyPayload(refs, actual, e.policy)
			if err != nil {
				return nil, err
			}
			// Compare the whole source and existing native scope, including tags
			// omitted from the source. Preflight and submission both use this path.
			native := map[string]map[string]any{}
			for name, other := range inventory {
				if other.unmapped {
					return nil, errConfigurationOwned
				}
				if name != tag.name {
					native[name] = other.fields
				}
			}
			if err := refuseWirelessTagScopeOverlap(payload, native, refs.sites, e.policy); err != nil {
				return nil, err
			}
			if e.policy && actual == nil {
				for _, other := range native {
					left := map[string]any{"siteIds": payload["siteIds"], "apZones": []string{"default-zone"}}
					right := map[string]any{"siteIds": other["siteIds"], "apZones": []string{"default-zone"}}
					shared, err := wirelessTagScopesConflict(left, right, refs.sites, true)
					if err != nil {
						return nil, err
					}
					if shared {
						return nil, errWirelessPolicyCohortRequired
					}
				}
			}

			if err := refuseWirelessTagScopeOverlap(payload, planned, refs.sites, e.policy); err != nil {
				return nil, err
			}
			planned[tag.name] = payload
			resource, err := wirelessTagResource(profile, tag, refs, payload, e.policy)
			if err != nil {
				return nil, err
			}
			var baseline any
			if e.comparison != nil {
				baseline, err = wirelessTagBaseline(profile, actual, refs, e.policy)
				if err != nil {
					return nil, err
				}
			}
			out = append(out, wirelessProfileTagObservation{resource: resource, profileID: profile.id, tagID: got.id, payload: payload, actual: actual, baseline: baseline})
		}
	}
	return out, nil
}
func wirelessTagRetainsMembers(old, next map[string]any) bool {
	for _, key := range []string{"siteIds", "apZones"} {
		prior, _ := old[key].([]string)
		desired, _ := next[key].([]string)
		set := map[string]bool{}
		for _, value := range desired {
			set[value] = true
		}
		for _, value := range prior {
			if !set[value] {
				return false
			}
		}
	}
	return true
}
func (e *wirelessProfileTagExecutor) next(ctx context.Context) (*configurationWrite, error) {
	rows, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	if write, err := e.planPolicyCohort(rows); err != nil || write != nil {
		return write, err
	}
	var first *configurationWrite
	for _, row := range rows {
		exists := row.actual != nil
		if exists && reflect.DeepEqual(row.payload, row.actual.fields) {
			continue
		}
		if exists && !wirelessTagRetainsMembers(row.actual.fields, row.payload) {
			if e.removal == nil {
				return nil, errConfigurationOwned
			}
			if err := e.removal(ctx, row); err != nil {
				return nil, err
			}
		}
		action, method, path := "Create", http.MethodPost, wirelessProfileTagPath(row.profileID, e.policy)+"/bulk"
		var body any = map[string]any{"items": []map[string]any{row.payload}}
		if exists {
			action, method, path = "Update", http.MethodPut, wirelessProfileTagPath(row.profileID, e.policy)+"/"+url.PathEscape(row.tagID)
			body = row.payload
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		write := &configurationWrite{resource: row.resource, action: action, identity: configurationResourceIdentity{parent: row.profileID, target: row.tagID}, submit: func(ctx context.Context) (Task, error) {
			raw, err := e.client.mutate(ctx, method, path, encoded)
			if err != nil {
				return Task{}, err
			}
			var receipt struct {
				Response Task `json:"response"`
			}
			if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
				return Task{}, errInvalidResponse
			}
			return receipt.Response, nil
		}}
		intent, err := json.Marshal(wirelessProfileDispatchIntent(e.desired))
		if err != nil {
			return nil, err
		}
		guardConfigurationPreflight(write, func(ctx context.Context) error {
			current, err := json.Marshal(wirelessProfileDispatchIntent(e.desired))
			if err != nil || !bytes.Equal(intent, current) {
				return errConfigurationStale
			}
			fresh, err := e.observe(ctx)
			if err != nil {
				return err
			}
			matched := false
			for _, now := range fresh {
				if now.resource == row.resource && now.profileID == row.profileID && now.tagID == row.tagID && reflect.DeepEqual(now.payload, row.payload) {
					if (now.actual == nil) != (row.actual == nil) {
						continue
					}
					if now.actual != nil && !reflect.DeepEqual(now.actual.fields, row.actual.fields) {
						continue
					}
					if now.actual != nil && !wirelessTagRetainsMembers(now.actual.fields, now.payload) {
						if e.removal == nil {
							return errConfigurationOwned
						}
						if err := e.removal(ctx, now); err != nil {
							return err
						}
					}
					matched = true
				}
			}
			if !matched {
				return errConfigurationStale
			}
			return nil
		})
		if first == nil {
			first = write
		}
	}
	return first, nil
}
func (e *wirelessProfileTagExecutor) verify(ctx context.Context, op *ciskov1.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.TargetSerial != "" || len(op.CredentialVersions) > 0 || !validAPIID(op.ParentID) || op.Action != "Create" && op.Action != "Update" || op.Action == "Create" && op.TargetID != "" || op.Action == "Update" && !validAPIID(op.TargetID) {
		return false, errConfigurationTransition
	}
	rows, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	if strings.HasPrefix(op.Resource, wirelessPolicyCohortPrefix) {
		return e.verifyPolicyCohort(rows, op)
	}
	for _, row := range rows {
		if row.resource != op.Resource {
			continue
		}
		if row.profileID != op.ParentID || op.TargetID != "" && row.tagID != op.TargetID {
			return false, errConfigurationStale
		}
		return row.actual != nil && reflect.DeepEqual(row.payload, row.actual.fields), nil
	}
	return false, errConfigurationStale
}

// preflight validates tags against the final parent and additive assignments,
// before allowing an earlier prerequisite write through the journal.
func (e *wirelessProfileTagExecutor) preflight(ctx context.Context) error {
	rows, err := e.observeDependencies(ctx, true)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.actual != nil && !wirelessTagRetainsMembers(row.actual.fields, row.payload) {
			if e.removal == nil {
				return errConfigurationOwned
			}
			if err := e.removal(ctx, row); err != nil {
				return err
			}
		}
	}
	return nil
}
func (e *wirelessProfileTagExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	rows, err := e.observeDependencies(ctx, true)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, row := range rows {
		if row.actual != nil && reflect.DeepEqual(row.actual.fields, row.payload) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if row.actual != nil {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "network_profiles", Resource: row.resource, Path: "/wireless", Change: change})
	}
	return out, nil
}
