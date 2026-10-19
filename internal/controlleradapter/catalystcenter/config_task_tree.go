// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// Task-tree completion is controller execution evidence, not a device
// convergence attestation. Free-form progress, data and failure text are never
// retained. SWIM's inventory-service child matching remains product-specific.
func (c *client) configurationTaskTreeComplete(ctx context.Context, id string, submitted, now time.Time) (bool, error) {
	completed, err := c.configurationTaskTreeCompletion(ctx, id, submitted, now)
	return !completed.IsZero(), err
}

// Return the latest terminal time across the entire validated rooted tree.
// An incomplete tree supplies no timestamp. Consumers performing strict
// freshness checks must also fence a completion time ahead of their clock,
// even when it is within the existing controller clock-skew allowance.
func (c *client) configurationTaskTreeCompletion(ctx context.Context, id string, submitted, now time.Time) (time.Time, error) {
	if !validAPIID(id) || submitted.IsZero() || now.Before(submitted) {
		return time.Time{}, errInvalidResponse
	}
	raw, err := c.get(ctx, taskPath+id+"/tree", nil, http.StatusOK)
	if err != nil {
		return time.Time{}, err
	}
	var envelope struct {
		Response []struct {
			ID     string `json:"id"`
			Parent string `json:"parentId"`
			Root   string `json:"rootId"`
			Code   string `json:"errorCode"`
			Start  *int64 `json:"startTime"`
			End    *int64 `json:"endTime"`
			Error  *bool  `json:"isError"`
		} `json:"response"`
	}
	if json.Unmarshal(raw, &envelope) != nil || len(envelope.Response) == 0 || len(envelope.Response) > 10000 {
		return time.Time{}, errInvalidResponse
	}
	parents := make(map[string]string, len(envelope.Response))
	complete := true
	var latest time.Time
	var failure *configurationTaskFailure
	for _, task := range envelope.Response {
		if !validAPIID(task.ID) || task.Error == nil || task.Start == nil || *task.Start < submitted.Add(-configurationTaskClockSkew).UnixMilli() || *task.Start > now.Add(configurationTaskClockSkew).UnixMilli() {
			return time.Time{}, errInvalidResponse
		}
		if _, found := parents[task.ID]; found {
			return time.Time{}, errInvalidResponse
		}
		if task.ID == id {
			if task.Parent != "" || (task.Root != "" && task.Root != id) {
				return time.Time{}, errInvalidResponse
			}
		} else if !validAPIID(task.Parent) || task.Root != id {
			return time.Time{}, errInvalidResponse
		}
		parents[task.ID] = task.Parent
		if *task.Error {
			if failure == nil {
				failure = newConfigurationTaskFailure(task.Code)
			}
		}
		if task.End == nil || *task.End == 0 {
			complete = false
		} else if *task.End < *task.Start || *task.End > now.Add(configurationTaskClockSkew).UnixMilli() {
			return time.Time{}, errInvalidResponse
		} else {
			ended := time.UnixMilli(*task.End).UTC()
			if ended.After(latest) {
				latest = ended
			}
		}
	}
	if _, found := parents[id]; !found {
		return time.Time{}, errInvalidResponse
	}
	// Resolve every parent chain to the requested root, rejecting cycles and
	// disconnected branches. Mark resolved paths to keep this linear in size.
	resolved := map[string]bool{id: true}
	for node := range parents {
		path := map[string]bool{}
		for !resolved[node] {
			parent, found := parents[node]
			if !found || path[node] {
				return time.Time{}, errInvalidResponse
			}
			path[node] = true
			node = parent
		}
		for member := range path {
			resolved[member] = true
		}
	}
	if failure != nil {
		return time.Time{}, failure
	}
	if !complete {
		return time.Time{}, nil
	}
	return latest, nil
}
