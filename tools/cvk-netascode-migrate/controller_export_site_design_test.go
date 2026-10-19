// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package main

import (
	"bytes"
	"encoding/json"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func exportSiteDesignSnapshot(t *testing.T) []byte {
	t.Helper()
	model := `catalyst_center:
  sites:
    floors:
      - name: Demo
        parent_name: Global/Lab/Building
        floor_number: 1
        image_path: maps/demo.pdf
        planned_access_points:
          - name: DesignAP
            ap_type: AP9130I
            position_x: 1
            position_y: 1
            position_z: 1
            radios: [{band: 5, antenna_name: Internal-9130-5GHz}]
`
	raw, err := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "data": map[string]string{"model.yaml": model, "cvk-floor-image-assets.json": `{"maps/demo.pdf":"demo-pdf"}`}, "binaryData": map[string][]byte{"demo-pdf": []byte("%PDF-1.7\ndemo")}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
func TestControllerExportSitesConfigMapSnapshot(t *testing.T) {
	raw := exportSiteDesignSnapshot(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "snapshot.json")
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "resolved.json")
	var out, errout bytes.Buffer
	args := []string{"controller-export-sites", "--model-version", "0.5.0", "--output", path, "--source-configmap", source, "--model-key", "model.yaml"}
	if code := run(args, nil, &out, &errout); code != 0 {
		t.Fatal(code, errout.String())
	}
	resolved, err := os.ReadFile(path)
	if err != nil || !bytes.Contains(resolved, []byte(`"image_path": "maps/demo.pdf"`)) || !bytes.Contains(resolved, []byte(`"planned_access_points"`)) || !bytes.Contains(resolved, []byte(`"width": 30`)) {
		t.Fatal("design/default declarations dropped", err)
	}
	var meta catalystcenter.SiteDefaultsProvenance
	if json.Unmarshal(out.Bytes(), &meta) != nil || meta.PlannedAccessPoints != 1 || meta.FloorImages != 1 || !meta.AssetsValidated || meta.AssetSnapshotSHA256 == "" || meta.SourceSnapshotSHA256 == "" || meta.ConfigurationParity || meta.LeafSchemaValidated {
		t.Fatal("invalid composition provenance")
	}
	if strings.Contains(out.String()+errout.String(), "DesignAP") || strings.Contains(out.String()+errout.String(), "maps/demo.pdf") {
		t.Fatal("model disclosed")
	}
	// stdin is explicit too; it remains one ConfigMap rather than two source streams.
	args[4] = filepath.Join(dir, "stdin-resolved.json")
	args[6] = "-"
	out.Reset()
	errout.Reset()
	if code := run(args, bytes.NewReader(raw), &out, &errout); code != 0 {
		t.Fatal(code, errout.String())
	}
}
func TestControllerExportSitesSnapshotRefusal(t *testing.T) {
	raw := exportSiteDesignSnapshot(t)
	for _, mode := range []string{"missing-key", "mixed-input", "key-without-snapshot", "wrong-kind", "unbound-image", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "absent.json")
			args := []string{"controller-export-sites", "--model-version", "0.5.0", "--output", path, "--source-configmap", "-", "--model-key", "model.yaml"}
			input := bytes.Clone(raw)
			switch mode {
			case "missing-key":
				args = args[:7]
			case "mixed-input":
				args = append(args, "other.yaml")
			case "key-without-snapshot":
				args = append(args[:5], "--model-key", "model.yaml")
			case "wrong-kind":
				input = bytes.Replace(input, []byte(`"ConfigMap"`), []byte(`"Secret"`), 1)
			case "unbound-image":
				input = bytes.Replace(input, []byte(`"demo-pdf":"JVBERi0xLjcKZGVtbw=="`), []byte(`"other":"TkVWRVJfUFJJTlRfVEhJUw=="`), 1)
			case "malformed":
				input = []byte(`{"apiVersion":"v1","kind":"ConfigMap","data":{},"data":{"x":"NEVER_PRINT_THIS"}}`)
			}
			var out, errout bytes.Buffer
			if run(args, bytes.NewReader(input), &out, &errout) == 0 {
				t.Fatal("invalid source/flags accepted")
			}
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("partial artifact emitted")
			}
			if strings.Contains(out.String()+errout.String(), "NEVER_PRINT_THIS") {
				t.Fatal("snapshot data disclosed")
			}
		})
	}
}
