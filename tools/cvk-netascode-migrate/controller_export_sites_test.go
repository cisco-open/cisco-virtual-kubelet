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
)

const exportSitesInput = `catalyst_center:
  sites:
    areas: [{name: Defaults, parent_name: Global}]
    floors: [{name: Floor, parent_name: Global/Defaults/ExistingBuilding, floor_number: 1}]
`

func TestControllerExportSitesPrivateArtifact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolved.json")
	args := []string{"controller-export-sites", "--model-version", "0.5.0", "--output", path}
	var out, errout bytes.Buffer
	if code := run(args, strings.NewReader(exportSitesInput), &out, &errout); code != 0 {
		t.Fatal(code, errout.String())
	}
	b, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(b, []byte(`"width": 30`)) {
		t.Fatal("resolved geometry missing", err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal("private file mode lost")
	}
	var meta catalystcenter.SiteDefaultsProvenance
	if json.Unmarshal(out.Bytes(), &meta) != nil || meta.Exporter != catalystcenter.SiteDefaultsExporter || !meta.ExecutionSyntaxValidated || meta.ConfigurationParity || meta.LeafSchemaValidated || meta.DefaultedFields != 5 {
		t.Fatal("wrong provenance")
	}
	if strings.Contains(out.String(), "ExistingBuilding") {
		t.Fatal("model printed to stdout")
	}
	out.Reset()
	errout.Reset()
	if run(args, strings.NewReader(exportSitesInput), &out, &errout) == 0 {
		t.Fatal("overwrote existing artifact")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(b, after) {
		t.Fatal("existing artifact modified")
	}
	link := filepath.Join(filepath.Dir(path), "link.json")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	args[4] = link
	if run(args, strings.NewReader(exportSitesInput), &out, &errout) == 0 {
		t.Fatal("followed output symlink")
	}
}
func TestControllerExportSitesRefusalAndBulkOption(t *testing.T) {
	for _, raw := range []string{strings.Replace(exportSitesInput, "floor_number: 1", "floor_number: 1, secret: NEVER_PRINT_THIS", 1), exportSitesInput + "---\nextra: 1", strings.Repeat("x", (1<<20)+1)} {
		path := filepath.Join(t.TempDir(), "absent.json")
		var out, errout bytes.Buffer
		if run([]string{"controller-export-sites", "--model-version", "0.5.0", "--output", path}, strings.NewReader(raw), &out, &errout) == 0 {
			t.Fatal("invalid model accepted")
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("partial file emitted")
		}
		if strings.Contains(out.String()+errout.String(), "NEVER_PRINT_THIS") {
			t.Fatal("input disclosed")
		}
	}
	for _, bulk := range []bool{false, true} {
		raw := strings.Replace(exportSitesInput, ", parent_name: Global}", "}", 1)
		args := []string{"controller-export-sites", "--model-version", "0.5.0", "--output", filepath.Join(t.TempDir(), "sites.json")}
		if bulk {
			args = append(args, "--use-bulk-api")
		}
		var out, errout bytes.Buffer
		if (run(args, strings.NewReader(raw), &out, &errout) == 0) != bulk {
			t.Fatal("bulk/singular selection changed")
		}
	}
	var out, errout bytes.Buffer
	if run([]string{"controller-export-sites", "--model-version", "latest", "--output", "-"}, strings.NewReader(exportSitesInput), &out, &errout) != 2 {
		t.Fatal("invalid flags accepted")
	}
}
