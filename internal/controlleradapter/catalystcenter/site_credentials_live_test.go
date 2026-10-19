// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func waitSiteCredentialTask(ctx context.Context, c *client, task Task, submitted time.Time) error {
	for {
		done, err := c.configurationTaskComplete(ctx, task.ID, submitted, time.Now())
		if err != nil || done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// A new empty area isolates inheritance changes from all existing device sites.
// Any uncertain operation leaves the area intact for supervised inspection.
func liveCredentialAssignmentSite(t *testing.T, c *client) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	name := "CVK-Cred-" + time.Now().UTC().Format("20060102-150405-000000000")
	path := "Global/" + name
	desired, err := parseSites([]byte(fmt.Sprintf(`{"catalyst_center":{"sites":{"areas":[{"name":%q,"parent_name":"Global"}]}}}`, name)))
	if err != nil {
		t.Fatal(err)
	}
	sites, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range sites {
		if s.NameHierarchy == path {
			t.Fatal("qualification site exists")
		}
	}
	m, err := nextSiteMutation(desired, sites)
	if err != nil || m == nil {
		t.Fatal("site plan failed", err)
	}
	submitted := time.Now()
	task, err := c.submitSiteMutation(ctx, m)
	if err != nil {
		t.Fatal("site submission uncertain", path, err)
	}
	t.Log("assignment fixture area task", path, task.ID)
	if err = waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
		t.Fatal("site task uncertain", path, err)
	}
	sites, err = c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	id := ""
	for _, s := range sites {
		if s.NameHierarchy == path {
			id = s.ID
		}
	}
	if !validAPIID(id) {
		t.Fatal("site readback missing")
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Log("assignment fixture retained for inspection", path, id)
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		sites, err := c.ListSites(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		found := false
		for _, s := range sites {
			if strings.HasPrefix(s.NameHierarchy, path+"/") || (s.NameHierarchy == path && s.ID != id) || (s.ID == id && s.NameHierarchy != path) {
				t.Error("site fixture changed; cleanup refused")
				return
			}
			if s.ID == id {
				found = true
			}
		}
		if !found {
			t.Error("site fixture missing")
			return
		}
		state, err := c.siteCredentials(ctx, id)
		if err != nil {
			t.Error(err)
			return
		}
		for _, slot := range state {
			if slot.id != "" {
				t.Error("credential override remains; cleanup refused")
				return
			}
		}
		submitted := time.Now()
		raw, err := c.mutate(ctx, http.MethodDelete, "/dna/intent/api/v1/areas/"+id, nil)
		if err != nil {
			t.Error(err)
			return
		}
		var receipt struct {
			Response Task `json:"response"`
		}
		if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
			t.Error("missing deletion receipt")
			return
		}
		if err = waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
			t.Error(err)
			return
		}
		sites, err = c.ListSites(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		for _, s := range sites {
			if s.ID == id || s.NameHierarchy == path {
				t.Error("fixture deletion not verified")
				return
			}
		}
		t.Log("empty credential assignment fixture removed", id, receipt.Response.ID)
	})
	return path
}
func qualifyLiveCredentialAssignment(t *testing.T, c *client, site string, want globalCredentialIntent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	desired := siteCredentialIntent{site: site, siteKind: "area", kind: want.kind.model, name: want.metadata.Name}
	selected := []siteCredentialIntent{desired}
	var selectedSource []byte
	if os.Getenv("CATC_CREDENTIAL_SITE_SELECTION") == "isolated-empty-area" {
		name := strings.TrimPrefix(site, "Global/")
		source, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{}, "sites": map[string]any{"areas": []any{map[string]any{"name": name, "parent_name": "Global", want.kind.model: want.metadata.Name}, map[string]any{"name": "CVK-Excluded-" + name, "parent_name": "Global", want.kind.model: "UNSELECTED_REFERENCE"}}}}})
		resolved, meta, err := ExportSelectedCredentials(source, nil, nacmodel.SiteSelectionOptions{ManagedSites: []string{site}, ManageSpecificSitesOnly: true})
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := parseNetworkReportIntent(resolved)
		if err != nil || len(parsed.credentials) != 0 || len(parsed.assignments) != 1 || meta.SiteSelection.SelectedDefinitions != 0 || !meta.SiteSelection.PerSiteMode {
			t.Fatal("per-site selection created credential definitions", err)
		}
		selected = parsed.assignments
		selectedSource = resolved
		t.Log("selected assignment uses existing named lookup; global definitions omitted", want.kind.model)
	}
	e := &siteCredentialExecutor{client: c, desired: selected}
	var op *ciskov1.NetworkControllerConfigurationOperation
	if os.Getenv("CATC_INSTALLED_SITE_CREDENTIAL_RECOVERY") == "isolated-empty-area" {
		op = qualifyInstalledSiteCredentialRecovery(t, c, selected, selectedSource)
		t.Log("installed assignment original task recovered", want.kind.model, op.TaskID)
	} else if os.Getenv("CATC_SITE_CREDENTIAL_RECOVERY") == "isolated-empty-area" {
		op = qualifyLiveSiteCredentialRecovery(t, c, selected, selectedSource)
		t.Log("public assignment original timeout task recovered", want.kind.model, op.TaskID)
	} else {
		write, err := e.next(ctx)
		if err != nil || write == nil {
			t.Fatal("assignment plan failed", err)
		}
		submitted := time.Now()
		task, err := write.submit(ctx)
		if err != nil {
			t.Fatal("assignment submission uncertain", err)
		}
		t.Log("site credential assignment task", want.kind.model, task.ID)
		if err = waitSiteCredentialTask(ctx, c, task, submitted); err != nil {
			t.Fatal(err)
		}
		op = &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action, TargetID: write.identity.target, ParentID: write.identity.parent}
		ok, err := e.verify(ctx, op)
		if err != nil || !ok {
			t.Fatal("assignment readback failed", err)
		}
	}
	next, err := e.next(ctx)
	if err != nil || next != nil {
		t.Fatal("assignment did not converge", err)
	}
	var verifyBulkCleanup func()
	if os.Getenv("CATC_WIRED_BULK_CREDENTIAL_EXECUTION") == "isolated-empty-area" {
		verifyBulkCleanup = qualifyLiveWiredBulkCredentialBinding(t, c, ctx, desired)
	}
	// Isolated fixture cleanup deliberately disables this local slot. This is
	// not parent inheritance and is not exposed through manifest execution.
	body, _ := json.Marshal(map[string]any{credentialSlot(want.kind.model): map[string]any{"credentialsId": nil}})
	submitted := time.Now()
	raw, err := c.mutate(ctx, http.MethodPut, siteCredentialsPath(op.TargetID), body)
	if err != nil {
		t.Fatal("cleanup uncertain", err)
	}
	var receipt struct {
		Response Task `json:"response"`
	}
	if json.Unmarshal(raw, &receipt) != nil || !validAPIID(receipt.Response.ID) {
		t.Fatal("missing cleanup receipt")
	}
	if err = waitSiteCredentialTask(ctx, c, receipt.Response, submitted); err != nil {
		t.Fatal(err)
	}
	state, err := c.siteCredentials(ctx, op.TargetID)
	if err != nil || !state[want.kind.model].unset {
		t.Fatal("isolated slot cleanup not verified", err)
	}
	if verifyBulkCleanup != nil {
		verifyBulkCleanup()
	}
	t.Log("assignment, no-op and isolated disabled-slot cleanup verified", want.kind.model, receipt.Response.ID)
}
