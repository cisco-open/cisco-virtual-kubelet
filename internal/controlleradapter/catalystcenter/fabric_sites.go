// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"sort"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

const fabricSitesPath = "/dna/intent/api/v1/sda/fabricSites"

type fabricSiteIntent struct {
	path, authentication string
	pubsub               bool
}
type fabricSite struct {
	ID             string `json:"id,omitempty"`
	SiteID         string `json:"siteId"`
	Authentication string `json:"authenticationProfileName"`
	PubSub         *bool  `json:"isPubSubEnabled"`
}

func validFabricAuthentication(name string) bool {
	switch name {
	case "Closed Authentication", "Low Impact", "No Authentication", "Open Authentication":
		return true
	}
	return false
}
func parseFabricSiteModels(value any) ([]fabricSiteIntent, error) {
	list, ok := value.([]any)
	if !ok || len(list) == 0 || len(list) > 1000 {
		return nil, errInvalidResponse
	}
	out := []fabricSiteIntent{}
	seen := map[string]bool{}
	for _, value := range list {
		model, ok := value.(map[string]any)
		if !ok {
			return nil, errInvalidResponse
		}
		for key := range model {
			if key != "name" && key != "pub_sub_enabled" && key != "authentication_template" {
				return nil, errInvalidResponse
			}
		}
		path, ok := model["name"].(string)
		if !ok || !validSitePath(path) || path == "Global" || seen[path] {
			return nil, errInvalidResponse
		}
		seen[path] = true
		pubsub, ok := model["pub_sub_enabled"].(bool)
		if !ok {
			return nil, errInvalidResponse
		}
		auth, ok := model["authentication_template"].(map[string]any)
		if !ok || len(auth) != 1 {
			return nil, errInvalidResponse
		}
		name, ok := auth["name"].(string)
		if !ok || !validFabricAuthentication(name) {
			return nil, errInvalidResponse
		}
		out = append(out, fabricSiteIntent{path: path, authentication: name, pubsub: pubsub})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}
func (s fabricSiteIntent) resource() string {
	return fmt.Sprintf("fabric-site:%x", sha256.Sum256([]byte(s.path)))
}
func (c *client) fabricSites(ctx context.Context) (map[string]fabricSite, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) { return c.get(ctx, path, q, 200) }
	rows, err := listInventoryPagesWithGet(ctx, get, fabricSitesPath, nil, func(s fabricSite) string { return s.ID }, 50)
	if err != nil {
		return nil, err
	}
	out := map[string]fabricSite{}
	for _, site := range rows {
		if !validAPIID(site.ID) || !validAPIID(site.SiteID) || !validFabricAuthentication(site.Authentication) || site.PubSub == nil {
			return nil, errInvalidResponse
		}
		if _, seen := out[site.SiteID]; seen {
			return nil, errInvalidResponse
		}
		out[site.SiteID] = site
	}
	return out, nil
}

type fabricSiteExecutor struct {
	client  *client
	desired []fabricSiteIntent
}
type fabricSiteState struct {
	want   fabricSiteIntent
	siteID string
	actual fabricSite
	exists bool
}

func (e *fabricSiteExecutor) count() int { return len(e.desired) }
func (e *fabricSiteExecutor) observe(ctx context.Context) ([]fabricSiteState, error) {
	sites, err := e.client.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	byPath := map[string]Site{}
	for _, site := range sites {
		byPath[site.NameHierarchy] = site
	}
	fabric, err := e.client.fabricSites(ctx)
	if err != nil {
		return nil, err
	}
	out := []fabricSiteState{}
	for _, want := range e.desired {
		site, ok := byPath[want.path]
		if !ok || site.Type == "global" {
			return nil, errInvalidResponse
		}
		actual, exists := fabric[site.ID]
		out = append(out, fabricSiteState{want: want, siteID: site.ID, actual: actual, exists: exists})
	}
	return out, nil
}
func fabricSiteMatches(want fabricSiteIntent, got fabricSite) bool {
	return got.PubSub != nil && *got.PubSub == want.pubsub && got.Authentication == want.authentication
}
func (e *fabricSiteExecutor) next(ctx context.Context) (*configurationWrite, error) {
	observations, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	var first *configurationWrite
	for _, o := range observations {
		if o.exists {
			if !fabricSiteMatches(o.want, o.actual) {
				return nil, errConfigurationTransition
			}
			continue
		}
		if first != nil {
			continue
		}
		body := fabricSite{SiteID: o.siteID, Authentication: o.want.authentication, PubSub: &o.want.pubsub}
		first = &configurationWrite{resource: o.want.resource(), action: "Create", identity: configurationResourceIdentity{parent: o.siteID}, submit: func(ctx context.Context) (Task, error) {
			return e.client.submitTask(ctx, fabricSitesPath, []fabricSite{body})
		}}
	}
	return first, nil
}
func (e *fabricSiteExecutor) verify(ctx context.Context, op *cvk.NetworkControllerConfigurationOperation) (bool, error) {
	if op == nil || op.Action != "Create" || op.TargetID != "" || !validAPIID(op.ParentID) || op.TargetSerial != "" || len(op.CredentialVersions) != 0 {
		return false, errConfigurationTransition
	}
	observations, err := e.observe(ctx)
	if err != nil {
		return false, err
	}
	for _, o := range observations {
		if o.want.resource() == op.Resource {
			if o.siteID != op.ParentID {
				return false, errConfigurationStale
			}
			return o.exists && fabricSiteMatches(o.want, o.actual), nil
		}
	}
	return false, errConfigurationTransition
}
func (e *fabricSiteExecutor) report(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
	observations, err := e.observe(ctx)
	if err != nil {
		return nil, err
	}
	out := []configapi.NetworkControllerDriftEntry{}
	for _, o := range observations {
		if o.exists && fabricSiteMatches(o.want, o.actual) {
			continue
		}
		change := configapi.NetworkControllerDriftChangeAdd
		if o.exists {
			change = configapi.NetworkControllerDriftChangeUpdate
		}
		out = append(out, configapi.NetworkControllerDriftEntry{Section: "fabric", Resource: o.want.resource(), Path: "/fabric_sites", Change: change})
	}
	return out, nil
}
