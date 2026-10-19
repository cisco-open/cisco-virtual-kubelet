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
	corev1 "k8s.io/api/core/v1"
)

func TestControllerExportSitesSelectedArtifact(t *testing.T) {
	raw := exportSiteDesignSnapshot(t)
	path := filepath.Join(t.TempDir(), "bound.json")
	args := []string{"controller-export-sites", "--model-version", "0.5.0", "--output", path, "--source-configmap", "-", "--model-key", "model.yaml", "--output-configmap", "--managed-sites", `["Global/Lab/Building/Demo"]`, "--manage-specific-sites-only"}
	var out, errout bytes.Buffer
	if code := run(args, bytes.NewReader(raw), &out, &errout); code != 0 {
		t.Fatal(code, errout.String())
	}
	result, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cm corev1.ConfigMap
	var meta catalystcenter.SiteDefaultsProvenance
	if json.Unmarshal(result, &cm) != nil || !strings.Contains(cm.Data["model.yaml"], "cvk_site_source") || !bytes.Equal(cm.BinaryData["demo-pdf"], []byte("%PDF-1.7\ndemo")) {
		t.Fatal("scope or assets lost")
	}
	if json.Unmarshal(out.Bytes(), &meta) != nil || !meta.FullSourceRetained || meta.SiteSelection.SelectedSiteCount != 1 || meta.ArtifactFormat != "v1/ConfigMap" {
		t.Fatal("bound artifact provenance missing")
	}
	if strings.Contains(out.String(), "Global/Lab") {
		t.Fatal("private scope printed")
	}
}
func TestControllerExportSitesSelectionRequiresBoundOutput(t *testing.T) {
	for _, args := range [][]string{
		{"--managed-sites", "[]"},
		{"--output-configmap"},
		{"--source-configmap", "-", "--model-key", "model.yaml", "--managed-sites", "[]"},
		{"--source-configmap", "-", "--model-key", "model.yaml", "--output-configmap", "--managed-sites", "null"},
	} {
		path := filepath.Join(t.TempDir(), "absent.json")
		command := append([]string{"controller-export-sites", "--model-version", "0.5.0", "--output", path}, args...)
		var out, errout bytes.Buffer
		if run(command, bytes.NewReader(exportSiteDesignSnapshot(t)), &out, &errout) != 2 {
			t.Fatal("invalid selection/output flags accepted", errout.String())
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("partial artifact emitted")
		}
	}
}
