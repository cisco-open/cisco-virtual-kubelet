// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Reuse site planning/task readers; never attach devices to this hierarchy.
// Cleanup is exact-UUID, child-before-parent and refuses an uncertain outcome.
func liveWiredBulkCredentialChild(t *testing.T, c *client, ctx context.Context, parent string) (map[string]any, string) {
	t.Helper()
	if !strings.HasPrefix(parent, "Global/CVK-Cred-") || strings.Count(parent, "/") != 1 {
		t.Fatal("credential child qualification requires generated empty area")
	}
	building := map[string]any{"name": "DependencyChild", "parent_name": parent, "country": "United States", "latitude": 37.3381234, "longitude": -121.8861234}
	path := parent + "/DependencyChild"
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": map[string]any{"buildings": []any{building}}}})
	desired, err := parseSites(raw)
	if err != nil {
		t.Fatal(err)
	}
	before, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, site := range before {
		if site.NameHierarchy == path {
			t.Fatal("credential child path already occupied")
		}
	}
	mutation, err := nextSiteMutation(desired, before)
	if err != nil || mutation == nil {
		t.Fatal("credential child plan unavailable", err)
	}
	submitted := time.Now()
	task, err := c.submitSiteMutation(ctx, mutation)
	if err != nil {
		t.Fatal("credential child submission uncertain", path, err)
	}
	t.Log("empty credential child creation task", path, task.ID)
	if err := waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
		t.Fatal("credential child task uncertain", path, err)
	}
	current, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var created *Site
	for _, site := range current {
		if site.NameHierarchy == path {
			copy := site
			created = &copy
		}
	}
	if created == nil || created.Type != "building" || !validAPIID(created.ID) {
		t.Fatal("credential child native identity unavailable", path)
	}
	if drift, err := planSites(desired, current); err != nil || len(drift) != 0 {
		t.Fatal("credential child attributes did not converge", err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("credential child retained for inspection", path, created.ID)
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		current, err := c.ListSites(cleanupCtx)
		if err != nil {
			t.Error(err)
			return
		}
		found := false
		for _, site := range current {
			if strings.HasPrefix(site.NameHierarchy, path+"/") || (site.NameHierarchy == path && site.ID != created.ID) {
				t.Error("credential child cleanup refused changed hierarchy")
				return
			}
			if site.ID == created.ID {
				if !reflect.DeepEqual(site, *created) {
					t.Error("credential child cleanup refused modified site")
					return
				}
				found = true
			}
		}
		if !found {
			t.Error("credential child disappeared before cleanup")
			return
		}
		slots, err := c.siteCredentials(cleanupCtx, created.ID)
		if err != nil {
			t.Error(err)
			return
		}
		for _, slot := range slots {
			if slot.id != "" {
				t.Error("credential child cleanup refused remaining local assignment")
				return
			}
		}
		submitted := time.Now()
		raw, err := c.mutate(cleanupCtx, http.MethodDelete, "/dna/intent/api/v2/buildings/"+created.ID, nil)
		if err != nil {
			t.Error("credential child cleanup uncertain", err)
			return
		}
		var ack struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &ack) != nil || !validAPIID(ack.Response.ID) {
			t.Error("credential child cleanup acknowledgement unavailable")
			return
		}
		if err := waitSiteCredentialTask(cleanupCtx, c, ack.Response, submitted); err != nil {
			t.Error("credential child cleanup task uncertain", err)
			return
		}
		after, err := c.ListSites(cleanupCtx)
		if err != nil {
			t.Error(err)
			return
		}
		for _, site := range after {
			if site.ID == created.ID || site.NameHierarchy == path {
				t.Error("credential child cleanup absence unverified")
				return
			}
		}
		t.Log("empty credential child removed and verified absent", created.ID, ack.Response.ID)
	})
	return building, path
}
