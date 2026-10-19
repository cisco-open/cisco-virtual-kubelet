// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sort"
)

var errTagPruneReferenced = errors.New("tag consumers are present or complete consumer evidence is unavailable")

// Preserve every returned detail attribute, including unknown/read-only fields.
// The fingerprint never authorizes adoption of an object by name.
func (c *client) tagNativeState(ctx context.Context, id string) (observedTag, string, error) {
	if !canonicalTemplateUUID(id) {
		return observedTag{}, "", errInvalidResponse
	}
	raw, err := c.get(ctx, tagsPath+"/"+url.PathEscape(id), nil, http.StatusOK)
	if err != nil {
		return observedTag{}, "", err
	}
	var env struct {
		Response json.RawMessage `json:"response"`
	}
	var fields map[string]any
	var row observedTag
	if json.Unmarshal(raw, &env) != nil || json.Unmarshal(env.Response, &fields) != nil || fields == nil || json.Unmarshal(env.Response, &row) != nil || row.ID != id || row.Name == "" || row.SystemTag == nil || *row.SystemTag {
		return row, "", errInvalidResponse
	}
	return row, featureIdentityDigest(fields), nil
}
func (c *client) tagConsumerTypes(ctx context.Context) ([]string, error) {
	raw, err := c.get(ctx, tagsPath+"/member/type", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	var env struct {
		Response []string `json:"response"`
	}
	if json.Unmarshal(raw, &env) != nil || len(env.Response) == 0 || len(env.Response) > 3 {
		return nil, errTagPruneReferenced
	}
	seen := map[string]bool{}
	for _, kind := range env.Response {
		if kind != "networkdevice" && kind != "interface" && kind != "template" || seen[kind] {
			return nil, errTagPruneReferenced
		}
		seen[kind] = true
	}
	// Template associations exist even on appliances which advertise only device
	// and interface resource types. Never infer their absence from that omission.
	if !seen["networkdevice"] || !seen["interface"] {
		return nil, errTagPruneReferenced
	}
	seen["template"] = true
	types := []string{}
	for kind := range seen {
		types = append(types, kind)
	}
	sort.Strings(types)
	return types, nil
}
func (c *client) tagConsumerCount(ctx context.Context, id, kind string) (int64, error) {
	raw, err := c.get(ctx, tagsPath+"/"+url.PathEscape(id)+"/member/count", url.Values{"memberType": {kind}}, http.StatusOK)
	if err != nil {
		return 0, err
	}
	var env struct {
		Response *int64 `json:"response"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Response == nil || *env.Response < 0 {
		return 0, errInvalidResponse
	}
	return *env.Response, nil
}
func (c *client) tagUnused(ctx context.Context, id string) ([]string, error) {
	types, err := c.tagConsumerTypes(ctx)
	if err != nil {
		return nil, err
	}
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		return c.get(ctx, path, q, http.StatusOK)
	}
	for _, kind := range types {
		count, err := c.tagConsumerCount(ctx, id, kind)
		if err != nil || count != 0 {
			return nil, errTagPruneReferenced
		}
		type member struct {
			ID string `json:"instanceUuid"`
		}
		rows, err := listInventoryQueryWithGet(ctx, get, tagsPath+"/"+url.PathEscape(id)+"/member", url.Values{"memberType": {kind}}, func(m member) string { return m.ID })
		if err != nil || len(rows) != 0 {
			return nil, errTagPruneReferenced
		}
		count, err = c.tagConsumerCount(ctx, id, kind)
		if err != nil || count != 0 {
			return nil, errTagPruneReferenced
		}
	}
	again, err := c.tagConsumerTypes(ctx)
	if err != nil || featureIdentityDigest(types) != featureIdentityDigest(again) {
		return nil, errTagPruneReferenced
	}
	return types, nil
}
