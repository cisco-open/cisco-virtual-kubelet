// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"errors"
	"net/url"
	"sort"
	"strings"
)

type SDWANSite struct {
	ID      uint32        `json:"id"`
	Routers []SDWANRouter `json:"routers,omitempty"`
}
type SDWANRouter struct {
	ChassisID                string            `json:"chassis_id"`
	Tags                     []string          `json:"tags,omitempty"`
	DeviceTemplate           string            `json:"device_template,omitempty"`
	DeviceVariables          map[string]string `json:"device_variables,omitempty"`
	ConfigurationGroup       string            `json:"configuration_group,omitempty"`
	ConfigurationGroupDeploy *bool             `json:"configuration_group_deploy,omitempty"`
	PolicyGroup              string            `json:"policy_group,omitempty"`
	PolicyGroupDeploy        *bool             `json:"policy_group_deploy,omitempty"`
	PolicyVariables          map[string]any    `json:"policy_variables,omitempty"`
	TopologyLabel            string            `json:"topology_label,omitempty"`
}

const tagPath = "/dataservice/v1/tags"

func compileTags(sites []SDWANSite) ([]Resource, error) {
	byTag := map[string]map[string]bool{}
	seenSite := map[uint32]bool{}
	seenDevice := map[string]bool{}
	for _, site := range sites {
		if site.ID == 0 || seenSite[site.ID] {
			return nil, errors.New("invalid or duplicate SD-WAN site ID")
		}
		seenSite[site.ID] = true
		for _, router := range site.Routers {
			if !validID.MatchString(router.ChassisID) || seenDevice[router.ChassisID] {
				return nil, errors.New("invalid or duplicate chassis ID")
			}
			seenDevice[router.ChassisID] = true
			for _, tag := range router.Tags {
				if tag == "" || len(tag) > 128 || strings.TrimSpace(tag) != tag {
					return nil, errors.New("invalid SD-WAN tag")
				}
				if byTag[tag] == nil {
					byTag[tag] = map[string]bool{}
				}
				byTag[tag][router.ChassisID] = true
			}
		}
	}
	names := make([]string, 0, len(byTag))
	for name := range byTag {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]Resource, 0, len(names))
	for _, name := range names {
		devices := make([]string, 0, len(byTag[name]))
		for id := range byTag[name] {
			devices = append(devices, id)
		}
		sort.Strings(devices)
		out = append(out, Resource{Key: "sites/tags/" + name, Name: name, Section: "sites", Type: "tag", Kind: "tag", TagDevices: devices, Body: map[string]any{"data": []any{map[string]any{"name": name, "description": name}}}})
	}
	return out, nil
}

func (c *Client) tagID(ctx context.Context, name string) (string, error) {
	var list []map[string]any
	if err := c.Request(ctx, "GET", tagPath, nil, &list); err != nil {
		return "", err
	}
	if list == nil {
		return "", ErrResponse
	}
	id := ""
	for _, item := range list {
		if item["name"] != name {
			continue
		}
		v, ok := item["id"].(string)
		if !ok || !validID.MatchString(v) || id != "" {
			return "", ErrResponse
		}
		id = v
	}
	return id, nil
}
func (c *Client) observeTag(ctx context.Context, r Resource) (*Observation, error) {
	id, e := c.tagID(ctx, r.Name)
	if e != nil || id == "" {
		return nil, e
	}
	var body map[string]any
	if e = c.Request(ctx, "GET", tagPath+"/"+url.PathEscape(id), nil, &body); e != nil {
		return nil, e
	}
	if body["name"] != r.Name {
		return nil, ErrResponse
	}
	return &Observation{ID: id, Body: body}, nil
}
func equalTag(r Resource, body map[string]any) (bool, error) {
	if body["name"] != r.Name || body["description"] != r.Name {
		return false, nil
	}
	devices := []string{}
	if rows, ok := body["tagAssociation"].([]any); ok {
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				return false, ErrResponse
			}
			id, ok := row["id"].(string)
			if !ok {
				return false, ErrResponse
			}
			devices = append(devices, id)
		}
	}
	sort.Strings(devices)
	if len(devices) != len(r.TagDevices) {
		return false, nil
	}
	for i, device := range devices {
		if device != r.TagDevices[i] {
			return false, nil
		}
	}
	return true, nil
}
func (c *Client) createTag(ctx context.Context, r Resource) (string, error) {
	if err := c.Request(ctx, "POST", tagPath, r.Body, nil); err != nil {
		return "", err
	}
	id, e := c.tagID(ctx, r.Name)
	if e != nil {
		return "", e
	}
	if id == "" {
		return "", ErrResponse
	}
	if len(r.TagDevices) > 0 {
		if e = c.associateTag(ctx, id, r.TagDevices, false); e != nil {
			return "", e
		}
	}
	return id, nil
}
func (c *Client) tagOverlap(ctx context.Context, self string, devices []string) (map[string][]string, error) {
	overlap := map[string][]string{}
	for _, id := range devices {
		if !validID.MatchString(id) {
			return nil, ErrResponse
		}
		var result struct {
			Data []struct {
				UUID string `json:"uuid"`
				Tags []struct {
					ID string `json:"id"`
				} `json:"tags"`
			} `json:"data"`
		}
		if e := c.Request(ctx, "GET", "/dataservice/system/device/vedges?uuid="+url.QueryEscape(id), nil, &result); e != nil {
			return nil, e
		}
		for _, device := range result.Data {
			if device.UUID != id {
				continue
			}
			for _, tag := range device.Tags {
				if tag.ID != self && validID.MatchString(tag.ID) {
					overlap[tag.ID] = append(overlap[tag.ID], id)
				}
			}
		}
	}
	return overlap, nil
}
func tagAssociation(id string, devices []string) map[string]any {
	objects := make([]any, 0, len(devices))
	for _, device := range devices {
		objects = append(objects, map[string]any{"id": device, "objectType": "DEVICE"})
	}
	return map[string]any{"tagId": id, "objects": objects}
}
func (c *Client) associateTag(ctx context.Context, id string, devices []string, remove bool) error {
	if !validID.MatchString(id) {
		return ErrResponse
	}
	path := tagPath + "/associate"
	data := []any{tagAssociation(id, devices)}
	if remove {
		path += "?operationType=DELETE"
	} else {
		overlap, e := c.tagOverlap(ctx, id, devices)
		if e != nil {
			return e
		}
		ids := make([]string, 0, len(overlap))
		for other := range overlap {
			ids = append(ids, other)
		}
		sort.Strings(ids)
		for _, other := range ids {
			data = append(data, tagAssociation(other, overlap[other]))
		}
	}
	var result struct {
		TaskID string `json:"taskId"`
	}
	if e := c.Request(ctx, "POST", path, map[string]any{"data": data}, &result); e != nil {
		return e
	}
	if result.TaskID != "" {
		return c.waitCustomApplicationTask(ctx, result.TaskID)
	}
	return nil
}
func (c *Client) updateTag(ctx context.Context, r Resource, o *Observation) error {
	current := map[string]bool{}
	if rows, ok := o.Body["tagAssociation"].([]any); ok {
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				return ErrResponse
			}
			id, ok := row["id"].(string)
			if !ok {
				return ErrResponse
			}
			current[id] = true
		}
	}
	desired := map[string]bool{}
	for _, id := range r.TagDevices {
		desired[id] = true
	}
	add := []string{}
	remove := []string{}
	for id := range desired {
		if !current[id] {
			add = append(add, id)
		}
	}
	for id := range current {
		if !desired[id] {
			remove = append(remove, id)
		}
	}
	sort.Strings(add)
	sort.Strings(remove)
	if len(add) > 0 {
		if e := c.associateTag(ctx, o.ID, r.TagDevices, false); e != nil {
			return e
		}
	}
	if len(remove) > 0 {
		if e := c.associateTag(ctx, o.ID, remove, true); e != nil {
			return e
		}
	}
	return nil
}
func (c *Client) deleteTag(ctx context.Context, r Resource, id string) error {
	if !validID.MatchString(id) {
		return ErrResponse
	}
	var body map[string]any
	if e := c.Request(ctx, "GET", tagPath+"/"+url.PathEscape(id), nil, &body); e != nil {
		return e
	}
	devices := []string{}
	if rows, ok := body["tagAssociation"].([]any); ok {
		for _, raw := range rows {
			row, ok := raw.(map[string]any)
			if !ok {
				return ErrResponse
			}
			device, ok := row["id"].(string)
			if !ok || !validID.MatchString(device) {
				return ErrResponse
			}
			devices = append(devices, device)
		}
	}
	if len(devices) > 0 {
		if e := c.associateTag(ctx, id, devices, true); e != nil {
			return e
		}
	}
	return c.Request(ctx, "DELETE", tagPath+"?tagId="+url.QueryEscape(id), nil, nil)
}
