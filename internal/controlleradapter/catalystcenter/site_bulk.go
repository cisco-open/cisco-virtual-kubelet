// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strconv"
	"sync/atomic"
)

const siteBulkPath = "/dna/intent/api/v1/sites/bulk"

type siteBulkPlan struct {
	body     []byte
	desired  []desiredSite
	existing map[string]string
}

// Reuse singular-site validation and payload construction. Existing divergent
// sites require ordinary updates: the bulk endpoint silently skips them.
func planSiteBulkCreate(desired []desiredSite, actual []Site) (*siteBulkPlan, error) {
	ordered, err := orderedBulkSites(desired)
	if err != nil {
		return nil, err
	}
	if err := validateSiteExecution(ordered); err != nil {
		return nil, err
	}
	drift, err := planSites(ordered, actual)
	if err != nil {
		return nil, err
	}
	for _, change := range drift {
		if string(change.Change) != "Add" {
			return nil, errConfigurationBusy
		}
	}
	if len(drift) == 0 {
		return nil, nil
	}
	existing := map[string]string{}
	for _, site := range actual {
		if !validAPIID(site.ID) {
			return nil, errInvalidResponse
		}
		existing[site.NameHierarchy] = site.ID
	}
	// Synthetic IDs are used only locally to reuse the leaf planner for parents
	// which the batch will create. The bulk wire format carries hierarchy paths.
	projection := append([]Site(nil), actual...)
	for i, site := range ordered {
		if _, exists := existing[site.path]; !exists {
			projection = append(projection, Site{ID: "bulk-parent-" + strconv.Itoa(i), Name: lastSiteName(site.path), NameHierarchy: site.path, Type: site.kind})
		}
	}
	rows := []map[string]any{}
	for _, site := range ordered {
		if _, exists := existing[site.path]; exists {
			continue
		}
		// Remove this prospective child so its payload is planned as Create.
		parents := []Site{}
		for _, item := range projection {
			if item.NameHierarchy != site.path {
				parents = append(parents, item)
			}
		}
		mutation, err := nextSiteMutation([]desiredSite{site}, parents)
		if err != nil || mutation == nil {
			if err == nil {
				err = errInvalidResponse
			}
			return nil, err
		}
		var row map[string]any
		if json.Unmarshal(mutation.body, &row) != nil {
			return nil, errInvalidResponse
		}
		delete(row, "parentId")
		row["parentNameHierarchy"], row["type"] = parentSitePath(site.path), site.kind
		if site.kind == "building" && row["country"] == nil {
			return nil, errInvalidResponse
		}
		rows = append(rows, row)
	}
	body, err := json.Marshal(rows)
	if err != nil {
		return nil, err
	}
	return &siteBulkPlan{body: body, desired: ordered, existing: existing}, nil
}

// Manifest callers wrap this native primitive with a retained member/anchor
// binding and a fresh owned Submitting journal check.
// Even a denied or uncertain submission consumes this one-use closure.
func (c *client) planGuardedSiteBulkCreate(ctx context.Context, desired []desiredSite, guard func(context.Context) error) (func(context.Context) (Task, error), error) {
	if guard == nil {
		return nil, errConfigurationTransition
	}
	actual, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	plan, err := planSiteBulkCreate(desired, actual)
	if err != nil || plan == nil {
		return nil, err
	}
	var used atomic.Bool
	return func(ctx context.Context) (Task, error) {
		if !used.CompareAndSwap(false, true) {
			return Task{}, errConfigurationTransition
		}
		current, err := c.ListSites(ctx)
		if err != nil {
			return Task{}, err
		}
		latest, err := planSiteBulkCreate(plan.desired, current)
		if err != nil {
			return Task{}, err
		}
		if latest == nil || !reflect.DeepEqual(plan, latest) {
			return Task{}, errConfigurationStale
		}
		if err := guard(ctx); err != nil {
			return Task{}, err
		}
		return c.submitSiteMutation(ctx, &siteMutation{action: "Create", method: http.MethodPost, endpoint: siteBulkPath, body: plan.body})
	}, nil
}
