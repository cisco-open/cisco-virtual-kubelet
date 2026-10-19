// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const networkSiteSelectionSource = `{"catalyst_center":{"sites":{
"global":{"network_settings":{"network":"global"}},
"areas":[{"name":"Lab","parent_name":"Global","network_settings":{"network":"shared"}},
{"name":"Lab2","parent_name":"Global","network_settings":{"network":"shared"}},
{"name":"Other","parent_name":"Global","network_settings":{"network":"excluded"}}],
"buildings":[{"name":"B","parent_name":"Global/Lab","network_settings":{"network":"shared"}}],
"floors":[{"name":"F","parent_name":"Global/Lab/B","network_settings":{"network":"shared"}}]},
"network_settings":{"network":[{"name":"global","banner":"global"},{"name":"shared","ntp_servers":["192.0.2.1"]},{"name":"excluded","banner":"excluded"}]}}}`

func TestNetworkExportSiteSelection(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options nacmodel.SiteSelectionOptions
		paths   []string
	}{
		{"default", nacmodel.SiteSelectionOptions{}, []string{"Global", "Global/Lab", "Global/Lab2", "Global/Other", "Global/Lab/B", "Global/Lab/B/F"}},
		{"global", nacmodel.SiteSelectionOptions{ManageGlobalSettings: true}, []string{"Global"}},
		{"prefix", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}}, []string{"Global/Lab", "Global/Lab2", "Global/Lab/B", "Global/Lab/B/F"}},
		{"exact", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageSpecificSitesOnly: true}, []string{"Global/Lab"}},
		{"floor", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab/B/F"}, ManageSpecificSitesOnly: true}, []string{"Global/Lab/B/F"}},
		{"combined", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Other"}, ManageGlobalSettings: true}, []string{"Global", "Global/Other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, meta, err := ExportSelectedNetworkSettings([]byte(networkSiteSelectionSource), nil, tc.options)
			if err != nil {
				t.Fatal(err)
			}
			bindings, err := parseNetworkSettings(out)
			if err != nil {
				t.Fatal(err)
			}
			paths := []string{}
			for _, b := range bindings {
				paths = append(paths, b.path)
			}
			slices.Sort(paths)
			slices.Sort(tc.paths)
			if !reflect.DeepEqual(paths, tc.paths) || meta.SiteSelection == nil || meta.SiteSelection.SelectedSiteCount != len(paths) || meta.OutputSHA256 != networkExportDigest(out) || meta.Exporter != NetworkSiteSelectionExporter || !meta.ExecutionSyntaxValidated || meta.LeafSchemaValidated || meta.ConfigurationParity {
				t.Fatal("selection or provenance mismatch")
			}
			if tc.name == "exact" && (strings.Contains(string(out), `"excluded"`) || strings.Contains(string(out), `"global"`)) {
				t.Fatal("unselected profiles retained")
			}
		})
	}
}

func TestNetworkExportSiteSelectionZeroAndRefusal(t *testing.T) {
	raw := []byte(strings.Replace(networkSiteSelectionSource, `"global":{"network_settings":{"network":"global"}},`, "", 1))
	raw = []byte(strings.Replace(string(raw), `{"name":"global","banner":"global"},`, "", 1))
	out, meta, err := ExportSelectedNetworkSettings(raw, nil, nacmodel.SiteSelectionOptions{ManageGlobalSettings: true})
	if err != nil || meta.SiteSelection == nil || meta.SiteSelection.SelectedSiteCount != 0 {
		t.Fatal("zero export failed", err)
	}
	if again, _, err := ExportSelectedNetworkSettings(out, nil, nacmodel.SiteSelectionOptions{ManageGlobalSettings: true}); err != nil || string(again) != string(out) {
		t.Fatal("zero export cannot be exported again", err)
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("zero selection reached native API")
		w.WriteHeader(503)
	})
	plan, err := c.compileConfigurationApply(out, []string{"network_settings"}, nil)
	if err != nil || plan.executor.count() != 0 {
		t.Fatal("zero source cannot compile", err)
	}
	if write, err := plan.executor.next(context.Background()); err != nil || write != nil {
		t.Fatal("zero source planned write", err)
	}
	reports, err := c.compileConfigReports(out, []string{"network_settings"})
	if err != nil || len(reports) != 1 || reports[0].count != 0 {
		t.Fatal("zero report failed", err)
	}
	if drift, err := reports[0].observe(context.Background()); err != nil || len(drift) != 0 {
		t.Fatal("zero report read native", err)
	}
	for _, bad := range []string{
		strings.Replace(networkSiteSelectionSource, `"banner":"excluded"`, `"unsupported":"NEVER_PRINT_THIS"`, 1),
		strings.Replace(networkSiteSelectionSource, `"network":"excluded"`, `"network":"missing"`, 1),
		strings.Replace(networkSiteSelectionSource, `"name":"Other"`, `"name":"Lab"`, 1),
	} {
		if out, meta, err := ExportSelectedNetworkSettings([]byte(bad), nil, nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageSpecificSitesOnly: true}); err == nil || len(out) != 0 || meta.Exporter != "" {
			t.Fatal("selection hid invalid excluded source")
		}
	}
	var decoded map[string]any
	if json.Unmarshal(out, &decoded) != nil {
		t.Fatal("invalid zero JSON")
	}
}

func TestNetworkExportSelectionRetainedLifecycle(t *testing.T) {
	for _, kind := range []string{"ntp", "dhcp", "dns", "timezone", "banner", "telemetry"} {
		for _, mode := range []string{"reselect", "accepted-null", "unknown-null", "verified-null", "corrupt-history", "missing-site"} {
			t.Run(kind+"/"+mode, func(t *testing.T) { exerciseNetworkSelection(t, kind, "scoped-"+mode, nil) })
		}
	}
}
