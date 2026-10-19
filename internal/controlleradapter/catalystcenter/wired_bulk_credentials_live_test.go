// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build live

package catalystcenter

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Source values are constructed only in this read-only qualification fixture.
// Production must receive original explicit declarations; observed metadata is
// never a substitute for owned credential write/version receipts.
func TestLiveCatalystCenterWiredBulkCredentialDependencies(t *testing.T) {
	c := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	rest, _, err := c.session(ctx)
	if err != nil {
		t.Fatal(err)
	}
	trace := &wiredBulkReadOnlyTransport{base: rest.HTTPClient.Transport}
	rest.HTTPClient.Transport = trace
	t.Cleanup(func() {
		if trace.attempts.Load() != 0 {
			t.Error("credential identity qualification attempted native mutation")
		}
	})
	members, _, _ := liveWiredBulkMembers(t, c, ctx)
	catalog, err := c.ListSites(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sites := map[string]Site{}
	for _, s := range catalog {
		sites[s.NameHierarchy] = s
	}
	metadata, err := c.globalCredentials(ctx)
	if err != nil {
		t.Fatal(err)
	}
	geometry := liveWiredBulkResolvedSites(t, catalog)
	global := map[string]any{}
	paths := map[string]bool{}
	rows := []any{}
	for _, member := range members {
		rows = append(rows, map[string]any{"name": member.desired.reference.Name, "device_ip": member.desired.reference.IP, "serial_number": member.desired.reference.Serial, "site": member.desired.site, "state": "REPROVISION"})
		for path := member.desired.site; path != ""; path = parentSitePath(path) {
			paths[path] = true
			if path == "Global" {
				break
			}
		}
	}
	assignments := 0
	for path := range paths {
		site, exists := sites[path]
		if !exists {
			t.Fatal("native device site ancestry incomplete")
		}
		slots, err := c.siteCredentials(ctx, site.ID)
		if err != nil {
			t.Fatal(err)
		}
		entry := global
		if path != "Global" {
			entry = nil
			for _, list := range geometry {
				for _, candidate := range list {
					if candidate["parent_name"].(string)+"/"+candidate["name"].(string) == path {
						entry = candidate
					}
				}
			}
			if entry == nil {
				t.Fatal("native ancestry absent from explicit source fixture")
			}
		}
		for _, kind := range globalCredentialKinds {
			state := slots[kind.model]
			if state.id == "" {
				continue
			}
			name := ""
			for _, credential := range metadata[kind.model] {
				if credential.ID == state.id {
					name = credential.Name
				}
			}
			if name == "" {
				t.Fatal("assigned native credential does not resolve to public metadata")
			}
			entry[kind.model] = name
			assignments++
		}
	}
	if assignments == 0 {
		t.Skip("no named native assignments available; credential dependency qualification remains incomplete")
	}
	// Marshal geometry separately so the special Global model can be included.
	rawGeometry, _ := json.Marshal(geometry)
	var declaredSites map[string]any
	if err := json.Unmarshal(rawGeometry, &declaredSites); err != nil {
		t.Fatal(err)
	}
	if len(global) > 0 {
		declaredSites["global"] = global
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": map[string]any{"devices": rows}, "sites": declaredSites, "network_settings": map[string]any{}}})
	if err != nil {
		t.Fatal(err)
	}
	source := &configSourceSnapshot{raw: raw}
	observers, err := c.wiredBulkDependencies(source)
	if err != nil || len(observers) != 1 {
		t.Fatal("explicit source dependency observer unavailable", err)
	}
	original, err := observers[0](ctx)
	if err != nil {
		t.Fatal("native credential inheritance does not match explicit source fixture", err)
	}
	current, err := observers[0](ctx)
	if err != nil || original != current {
		t.Fatal("native dependency identity changed during qualification", err)
	}
	model, err := compileWiredBulkSource(source)
	if err != nil {
		t.Fatal(err)
	}
	model.network.assignments[0].name = "cvk-nonexistent-credential-qualification"
	if _, err := c.observeWiredBulkCredentials(ctx, model); err == nil {
		t.Fatal("missing declared external credential accepted")
	}
	if current, err = observers[0](ctx); err != nil || original != current {
		t.Fatal("original dependency state changed after refusal", err)
	}
	t.Logf("three lab identities, %d explicit named assignments: repeated original dependency identity and absent-name refusal verified; native mutations=0; no credential write, password-version or device-authentication proof claimed", assignments)
}

// Called only while an explicitly enabled, generated empty-area credential
// assignment fixture exists. This adds dependency reads to the existing native
// create/rotate/assign/cleanup test, not another mutation implementation. The
// synthetic inventory entry selects only the empty area for dependency reads;
// no physical device is looked up, provisioned or claimed by this check.
func qualifyLiveWiredBulkCredentialBinding(t *testing.T, c *client, ctx context.Context, assignment siteCredentialIntent) func() {
	t.Helper()
	if assignment.siteKind != "area" || !strings.HasPrefix(assignment.site, "Global/CVK-Cred-") || assignment.name == "" {
		t.Fatal("bulk credential qualification requires generated empty-area fixture")
	}
	area := map[string]any{"name": strings.TrimPrefix(assignment.site, "Global/"), "parent_name": "Global", assignment.kind: assignment.name}
	declaredSites := map[string]any{"areas": []any{area}}
	deviceSite := assignment.site
	if os.Getenv("CATC_WIRED_BULK_CREDENTIAL_INHERITANCE") == "isolated-child-building" {
		building, path := liveWiredBulkCredentialChild(t, c, ctx, assignment.site)
		declaredSites["buildings"] = []any{building}
		deviceSite = path
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{
		"inventory": map[string]any{"devices": []any{map[string]any{"name": "qualification-no-device", "site": deviceSite, "state": "REPROVISION"}}},
		"sites":     declaredSites, "network_settings": map[string]any{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	observers, err := c.wiredBulkDependencies(&configSourceSnapshot{raw: raw})
	if err != nil || len(observers) != 1 {
		t.Fatal("isolated external assignment source rejected", err)
	}
	before, err := observers[0](ctx)
	if err != nil {
		t.Fatal("settled native assignment dependency unavailable", err)
	}
	after, err := observers[0](ctx)
	if err != nil || before != after {
		t.Fatal("settled native dependency identity changed", err)
	}
	t.Log("isolated named credential assignment bound to stable bulk dependency identity", assignment.kind)
	if deviceSite != assignment.site {
		t.Log("native child-site credential inheritance bound through parent UUID ancestry", assignment.kind)
	}
	return func() {
		if _, err := observers[0](ctx); err == nil {
			t.Fatal("disabled native assignment retained original bulk dependency identity")
		}
		t.Log("original bulk dependency refused after exact isolated assignment cleanup", assignment.kind)
	}
}
