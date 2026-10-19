// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"strings"
	"testing"
)

func credentialSelectionSource(t *testing.T) []byte {
	t.Helper()
	groups := map[string]any{}
	for _, k := range globalCredentialKinds {
		var entry map[string]any
		if json.Unmarshal([]byte(globalCredentialFixtureEntries[k.model]), &entry) != nil {
			t.Fatal("fixture")
		}
		groups[k.model] = []any{entry}
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{"device_credentials": groups}, "sites": map[string]any{"areas": []any{map[string]any{"name": "Global", "snmpv3_credentials": "fixture"}, map[string]any{"name": "Lab", "parent_name": "Global", "cli_credentials": "fixture"}, map[string]any{"name": "Lab2", "parent_name": "Global", "cli_credentials": "fixture"}}, "buildings": []any{map[string]any{"name": "Building", "parent_name": "Global/Lab", "https_read_credentials": "fixture"}}}}})
	return raw
}
func TestCredentialExportSiteSelection(t *testing.T) {
	for _, tc := range []struct {
		name              string
		options           nacmodel.SiteSelectionOptions
		defs, assignments int
	}{
		{"single", nacmodel.SiteSelectionOptions{}, 6, 4},
		{"global", nacmodel.SiteSelectionOptions{ManageGlobalSettings: true}, 6, 1},
		{"prefix", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}}, 0, 3},
		{"exact", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageSpecificSitesOnly: true}, 0, 1},
		{"global-and-prefix", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageGlobalSettings: true}, 6, 4},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := credentialSelectionSource(t)
			out, m, err := ExportSelectedCredentials(raw, nil, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			if m.SiteSelection == nil || m.SiteSelection.SelectedDefinitions != tc.defs || m.SiteSelection.SelectedAssignments != tc.assignments || m.SiteSelection.DeclaredDefinitions != 6 || m.Resources != tc.defs+tc.assignments || m.OutputSHA256 != networkExportDigest(out) || m.InputSHA256 != networkExportDigest(raw) || !m.ExecutionSyntaxValidated || m.ConfigurationParity || m.LeafSchemaValidated {
				t.Fatal("selection provenance mismatch", m.SiteSelection)
			}
			if tc.defs == 0 {
				if strings.Contains(string(out), "device_credentials") || strings.Contains(string(out), "_version") {
					t.Fatal("per-site output retained credential creation")
				}
				intent, err := parseNetworkReportIntent(out)
				if err != nil || len(intent.assignments) != tc.assignments || len(intent.credentials) != 0 {
					t.Fatal("wrong runtime assignment selection", err)
				}
			} else {
				defs, _, err := splitNetworkCredentialConfiguration(out)
				if err != nil || len(defs) != 6 {
					t.Fatal("global definitions lost", err)
				}
			}
		})
	}
}
func TestCredentialSelectionRefusesHiddenInvalidInput(t *testing.T) {
	raw := string(credentialSelectionSource(t))
	opts := nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageSpecificSitesOnly: true}
	for _, bad := range []string{
		strings.Replace(raw, `"port":443`, `"port":null`, 1),
		strings.Replace(raw, `"password_version":1`, `"password":"PRIVATE_SECRET"`, 1),
		strings.Replace(raw, `"snmpv3_credentials":"fixture"`, `"snmpv3_credentials":null`, 1),
		strings.Replace(raw, `"name":"Lab2"`, `"name":"Lab2","latitude":1`, 1),
		strings.Replace(raw, `"name":"Lab2"`, `"name":"Lab2","unknown":true`, 1),
		strings.Replace(raw, `"sites":`, `"inventory":{},"sites":`, 1),
	} {
		out, m, err := ExportSelectedCredentials([]byte(bad), nil, opts)
		if err == nil || out != nil || m.Exporter != "" || strings.Contains(err.Error(), "PRIVATE_SECRET") {
			t.Fatal("hidden unsupported input escaped")
		}
	}
	if _, _, err := ExportSelectedCredentials([]byte(raw), nil, nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Missing"}}); err == nil {
		t.Fatal("undeclared managed path accepted")
	}
	// References to existing credentials do not need local definitions or secrets.
	out, m, err := ExportSelectedCredentials(siteCredentialModel("example"), nil, opts)
	if err != nil || m.SiteSelection.DeclaredDefinitions != 0 || m.SiteSelection.SelectedAssignments != 6 {
		t.Fatal("lookup-only model rejected", err)
	}
	if strings.Contains(string(out), "device_credentials") {
		t.Fatal("lookup-only source invented definitions")
	}
}
func TestSiteCredentialSelectedManifest(t *testing.T) {
	siteCredentialManifestCycle(t, false, siteCredentialManifestQualification{selected: true})
}
