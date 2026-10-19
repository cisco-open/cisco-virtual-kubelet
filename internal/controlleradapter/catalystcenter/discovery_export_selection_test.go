// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func discoverySelectionSource(t *testing.T, raw []byte) []byte {
	t.Helper()
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		t.Fatal("fixture JSON")
	}
	m := root["catalyst_center"].(map[string]any)
	m["sites"] = map[string]any{"areas": []any{map[string]any{"name": "Lab", "parent_name": "Global"}}}
	inventory := m["inventory"].(map[string]any)
	rows := inventory["discovery"].([]any)
	rows[0].(map[string]any)["create_per_site"] = true
	copy := map[string]any{}
	for k, v := range rows[0].(map[string]any) {
		copy[k] = v
	}
	copy["name"] = "excluded-global"
	copy["create_per_site"] = false
	inventory["discovery"] = append(rows, copy)
	out, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func selectedDiscoveryFixture(t *testing.T, raw []byte) []byte {
	t.Helper()
	out, _, err := ExportSelectedDiscovery(discoverySelectionSource(t, raw), nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}})
	if err != nil {
		t.Fatal(err)
	}
	var compact bytes.Buffer
	if json.Compact(&compact, out) != nil {
		t.Fatal("export JSON")
	}
	return compact.Bytes()
}
func TestDiscoveryExportSiteSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		opts    nacmodel.SiteSelectionOptions
		count   int
		perSite bool
	}{
		{"single-state", nacmodel.SiteSelectionOptions{}, 2, false},
		{"global", nacmodel.SiteSelectionOptions{ManageGlobalSettings: true}, 2, false},
		{"per-site", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}}, 1, true},
		{"exact-site", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageSpecificSitesOnly: true}, 1, true},
		{"global-and-sites", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageGlobalSettings: true}, 2, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := discoverySelectionSource(t, discoveryModel())
			out, meta, err := ExportSelectedDiscovery(raw, tc.opts)
			if err != nil {
				t.Fatal(err)
			}
			intent, err := parseInventoryIntent(out)
			if err != nil || len(intent.discoveries) != tc.count {
				t.Fatal("wrong runtime discovery selection", err)
			}
			if strings.Contains(string(out), "create_per_site") || strings.Contains(string(out), `"sites"`) {
				t.Fatal("deployment metadata became runtime intent")
			}
			if meta.DeclaredJobs != 2 || meta.SelectedJobs != tc.count || meta.PerSiteMode != tc.perSite || !meta.ExecutionSyntaxValidated || meta.LeafSchemaValidated || meta.ConfigurationParity || meta.OutputSHA256 != networkExportDigest(out) || meta.Exporter != DiscoverySiteSelectionExporter {
				t.Fatal("incorrect provenance")
			}
			for _, job := range intent.discoveries {
				if job.name != "fixture" && job.name != "excluded-global" {
					t.Fatal("site selection renamed a globally unique job")
				}
			}
		})
	}
}
func TestDiscoveryExportSelectionRefusesHiddenInvalidInput(t *testing.T) {
	raw := string(discoverySelectionSource(t, discoveryModel()))
	for _, bad := range []string{
		strings.Replace(raw, `"create_per_site":true`, `"create_per_site":null`, 1),
		strings.Replace(raw, `"excluded-global"`, `"fixture"`, 1),
		strings.Replace(raw, `"create_per_site":false`, `"create_per_site":false,"password_list":["NEVER_PRINT_SECRET"]`, 1),
		strings.Replace(raw, `"create_per_site":false`, `"create_per_site":false,"unsupported":"NEVER_PRINT_SECRET"`, 1),
		strings.Replace(raw, `"sites":`, `"wireless":{},"sites":`, 1),
		strings.Replace(raw, `"discovery":`, `"tags":[],"discovery":`, 1),
		strings.Replace(raw, `"parent_name":"Global"`, `"parent_name":"NEVER_PRINT_SECRET"`, 1),
	} {
		out, meta, err := ExportSelectedDiscovery([]byte(bad), nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}})
		if err == nil || len(out) != 0 || meta.Exporter != "" || strings.Contains(err.Error(), "NEVER_PRINT_SECRET") {
			t.Fatal("invalid excluded input was hidden or leaked")
		}
	}
	for _, opts := range []nacmodel.SiteSelectionOptions{{ManagedSites: []string{"Global/Missing"}}, {ManageSpecificSitesOnly: true}} {
		if out, meta, err := ExportSelectedDiscovery([]byte(raw), opts); err == nil || len(out) != 0 || meta.Exporter != "" {
			t.Fatal("unresolved site selection accepted")
		}
	}
	out, meta, err := ExportSelectedDiscovery([]byte(strings.Replace(raw, `"create_per_site":true`, `"create_per_site":false`, 1)), nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}})
	if err != nil || meta.SelectedJobs != 0 {
		t.Fatal("explicit empty selection failed", err)
	}
	intent, err := parseInventoryIntent(out)
	if err != nil || len(intent.discoveries) != 0 {
		t.Fatal("empty export did not use normal zero intent", err)
	}
	if _, err := parseInventoryIntent([]byte(raw)); err == nil {
		t.Fatal("runtime silently interpreted unresolved site selection")
	}
}
func TestDiscoverySelectedManifestLifecycle(t *testing.T) {
	discoveryManifestScenario(t, discoveryCycleScenario{replacement: true, selected: true})
}
