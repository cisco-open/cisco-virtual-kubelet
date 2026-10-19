// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const exportNetworkInput = `catalyst_center:
  sites:
    areas: [{name: Lab, parent_name: Global, network_settings: {telemetry: base}}]
  network_settings:
    telemetry: [{name: base, wired_data_collection: false, snmp_servers: [], syslog_servers: []}]
`

func TestControllerExportNetworkPrivateArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolved.json")
	args := []string{"controller-export-network", "--model-version", "0.5.0", "--output", path}
	var stdout, stderr bytes.Buffer
	if code := run(args, strings.NewReader(exportNetworkInput), &stdout, &stderr); code != 0 {
		t.Fatal("export failed", code, stderr.String())
	}
	data, err := os.ReadFile(path)
	if err != nil || catalystcenter.ValidateNetworkSettingsSource(data) != nil {
		t.Fatal("export does not use actual compiler", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("export is not private")
	}
	var meta nacmodel.NetworkDefaultsProvenance
	if json.Unmarshal(stdout.Bytes(), &meta) != nil || meta.ConfigurationParity || !meta.ExecutionSyntaxValidated || meta.LeafSchemaValidated || meta.Exporter != nacmodel.NetworkDefaultsExporter || meta.DefaultsSHA256 != nacmodel.DefaultsSHA256 || meta.ResolutionScope != "network-settings-profiles-only" {
		t.Fatal("invalid provenance")
	}
	if strings.Contains(stdout.String(), "wired_data_collection") || strings.Contains(stderr.String(), "base") {
		t.Fatal("payload disclosed")
	}
	stdout.Reset()
	stderr.Reset()
	if run(args, strings.NewReader(exportNetworkInput), &stdout, &stderr) == 0 {
		t.Fatal("overwrote artifact")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(data, after) {
		t.Fatal("existing artifact changed")
	}
}
func TestControllerExportNetworkRefusalAndSymlinks(t *testing.T) {
	for _, model := range []string{`{"catalyst_center":{"network_settings":{"network":[{"name":"base","banner":"NEVER_PRINT_THIS","unknown":"NEVER_PRINT_THIS"}]},"sites":{"global":{"network_settings":{"network":"base"}}}}}`, strings.Replace(exportNetworkInput, "wired_data_collection: false", "wired_data_collection: NEVER_PRINT_THIS", 1), strings.Repeat("x", (1<<20)+1)} {
		path := filepath.Join(t.TempDir(), "absent.json")
		var out, err bytes.Buffer
		if run([]string{"controller-export-network", "--model-version", "0.5.0", "--output", path}, strings.NewReader(model), &out, &err) == 0 {
			t.Fatal("invalid source exported")
		}
		if strings.Contains(out.String()+err.String(), "NEVER_PRINT_THIS") {
			t.Fatal("input disclosed")
		}
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatal("failed validation wrote artifact")
		}
	}
	directory := t.TempDir()
	target := filepath.Join(directory, "existing")
	link := filepath.Join(directory, "link")
	if os.WriteFile(target, []byte("unchanged"), 0600) != nil || os.Symlink(target, link) != nil {
		t.Fatal("fixture unavailable")
	}
	var out, err bytes.Buffer
	if run([]string{"controller-export-network", "--model-version", "0.5.0", "--output", link}, strings.NewReader(exportNetworkInput), &out, &err) == 0 {
		t.Fatal("followed output symlink")
	}
	data, _ := os.ReadFile(target)
	if string(data) != "unchanged" {
		t.Fatal("symlink target overwritten")
	}
	for _, args := range [][]string{{"controller-export-network"}, {"controller-export-network", "--model-version", "latest", "--output", filepath.Join(directory, "x")}, {"controller-export-network", "--model-version", "0.5.0", "--output", "-"}} {
		out.Reset()
		err.Reset()
		if run(args, strings.NewReader(exportNetworkInput), &out, &err) != 2 {
			t.Fatal("missing exact version/output guard")
		}
	}
}

func TestControllerExportNetworkDefaultsFile(t *testing.T) {
	directory := t.TempDir()
	defaults := filepath.Join(directory, "defaults.yaml")
	output := filepath.Join(directory, "resolved.json")
	if os.WriteFile(defaults, []byte(`defaults: {catalyst_center: {network_settings: {telemetry: {wireless_telemetry: true}}}}`), 0600) != nil {
		t.Fatal("fixture unavailable")
	}
	var stdout, stderr bytes.Buffer
	args := []string{"controller-export-network", "--model-version", "0.5.0", "--defaults", defaults, "--output", output}
	if run(args, strings.NewReader(exportNetworkInput), &stdout, &stderr) != 0 {
		t.Fatal("default override did not export", stderr.String())
	}
	var model map[string]any
	raw, _ := os.ReadFile(output)
	if json.Unmarshal(raw, &model) != nil {
		t.Fatal("invalid export")
	}
	profile := model["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["telemetry"].([]any)[0].(map[string]any)
	if profile["wireless_telemetry"] != true || profile["wired_data_collection"] != false {
		t.Fatal("file override/source precedence lost")
	}
	var meta nacmodel.NetworkDefaultsProvenance
	if json.Unmarshal(stdout.Bytes(), &meta) != nil || meta.OverridesSHA256 == "" || meta.PolicySHA256 == "" {
		t.Fatal("override/policy provenance missing")
	}
	if os.WriteFile(defaults, nil, 0600) != nil {
		t.Fatal("fixture update unavailable")
	}
	args[len(args)-1] = filepath.Join(directory, "absent.json")
	stdout.Reset()
	stderr.Reset()
	if run(args, strings.NewReader(exportNetworkInput), &stdout, &stderr) == 0 {
		t.Fatal("empty explicit defaults accepted")
	}
	if _, err := os.Stat(args[len(args)-1]); !os.IsNotExist(err) {
		t.Fatal("empty defaults wrote artifact")
	}
}

func TestControllerExportNetworkSiteSelection(t *testing.T) {
	for _, tc := range []struct {
		name  string
		flags []string
		count int
		bad   bool
	}{
		{"exact", []string{"--managed-sites", `["Global/Lab"]`, "--manage-specific-sites-only"}, 1, false},
		{"repeated", []string{"--managed-sites", `["Global/Lab","Global/Lab"]`, "--manage-specific-sites-only"}, 1, false},
		{"empty", []string{"--manage-global-settings"}, 0, false},
		{"false-is-explicit", []string{"--manage-global-settings=false"}, 1, false},
		{"missing", []string{"--managed-sites", `["Global/Absent"]`}, 0, true},
		{"root", []string{"--managed-sites", `["Global"]`}, 0, true},
		{"null", []string{"--managed-sites", `null`}, 0, true},
		{"empty-string", []string{"--managed-sites", ""}, 0, true},
		{"no-exact-sites", []string{"--manage-specific-sites-only"}, 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "selected.json")
			args := append([]string{"controller-export-network", "--model-version", "0.5.0", "--output", path}, tc.flags...)
			var stdout, stderr bytes.Buffer
			code := run(args, strings.NewReader(exportNetworkInput), &stdout, &stderr)
			if tc.bad {
				if code == 0 {
					t.Fatal("invalid selection exported")
				}
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatal("refusal wrote output")
				}
				return
			}
			if code != 0 {
				t.Fatal("selection failed", stderr.String())
			}
			var meta nacmodel.NetworkDefaultsProvenance
			if json.Unmarshal(stdout.Bytes(), &meta) != nil || meta.Exporter != catalystcenter.NetworkSiteSelectionExporter || meta.SiteSelection == nil || meta.SiteSelection.SelectedSiteCount != tc.count || !meta.ExecutionSyntaxValidated || meta.ConfigurationParity {
				t.Fatal("selection provenance mismatch")
			}
			if strings.Contains(stdout.String()+stderr.String(), "Global/Lab") {
				t.Fatal("site values exposed in provenance")
			}
			data, err := os.ReadFile(path)
			if err != nil || catalystcenter.ValidateNetworkSettingsSource(data) != nil {
				t.Fatal("selected output invalid", err)
			}
			info, _ := os.Stat(path)
			if info.Mode().Perm() != 0600 {
				t.Fatal("selected output not private")
			}
		})
	}
}
