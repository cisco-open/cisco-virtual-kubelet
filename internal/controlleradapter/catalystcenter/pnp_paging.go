// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
)

// PnP collections use bare arrays and zero-based offsets. Marshal only the
// caller's safe typed projection before the existing shared paging checks.
func listPnPPages[T any](ctx context.Context, c *client, path string, identity func(T) string, pageSize int) ([]T, error) {
	get := func(ctx context.Context, path string, q url.Values) ([]byte, error) {
		offset, err := strconv.Atoi(q.Get("offset"))
		if err != nil || offset < 1 {
			return nil, errInvalidResponse
		}
		query := url.Values{}
		for key, values := range q {
			query[key] = append([]string(nil), values...)
		}
		query.Set("offset", strconv.Itoa(offset-1))
		raw, err := c.get(ctx, path, query, 200)
		if err != nil {
			return nil, err
		}
		var page []T
		if json.Unmarshal(raw, &page) != nil || page == nil {
			return nil, errInvalidResponse
		}
		return json.Marshal(map[string]any{"response": page})
	}
	return listInventoryPagesWithGet(ctx, get, path, nil, identity, pageSize)
}
func (c *client) pnpCollectionCount(ctx context.Context, path string) (int, error) {
	raw, err := c.get(ctx, path+"/count", nil, 200)
	if err != nil {
		return 0, err
	}
	var env struct {
		Response *int `json:"response"`
	}
	if json.Unmarshal(raw, &env) != nil || env.Response == nil || *env.Response < 0 || *env.Response > 10000 {
		return 0, errInvalidResponse
	}
	return *env.Response, nil
}
