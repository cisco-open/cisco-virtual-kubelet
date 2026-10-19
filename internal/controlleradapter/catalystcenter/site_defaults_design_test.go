// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	yamlv3 "go.yaml.in/yaml/v3"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func siteDesignSnapshotFixture(t *testing.T) *corev1.ConfigMap {
	t.Helper()
	model := strings.Replace(plannedAPFixture, `"floor_number":1`, `"floor_number":1,"image_path":"maps/floor.pdf"`, 1)
	var root map[string]any
	if json.Unmarshal([]byte(model), &root) != nil {
		t.Fatal("fixture")
	}
	floor := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
	for _, field := range []string{"width", "length", "height", "rf_model", "units_of_measure"} {
		delete(floor, field)
	}
	raw, _ := json.Marshal(root)
	cm := floorImageAssetFixture()
	cm.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}
	cm.Data["model.yaml"] = string(raw)
	return cm
}
func TestSiteDefaultsDesignSnapshot(t *testing.T) {
	cm := siteDesignSnapshotFixture(t)
	raw, _ := json.Marshal(cm)
	before := bytes.Clone(raw)
	out, meta, err := ExportSiteDefaultsConfigMap(raw, "model.yaml", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	assets, err := loadFloorImageAssets(cm, "model.yaml")
	if err != nil {
		t.Fatal(err)
	}
	sites, aps, images, err := parseSitesWithFloorImages(out, assets)
	if err != nil || len(sites) != 1 || len(aps) != 1 || len(images) != 1 || meta.Resources != 1 || meta.PlannedAccessPoints != 1 || meta.FloorImages != 1 || meta.DefaultedFields != 5 || !meta.AssetsValidated || meta.ImageAssets != 1 || meta.AssetSnapshotSHA256 == "" || meta.SourceSnapshotSHA256 == "" || meta.ResolutionScope != "sites-design-snapshot" {
		t.Fatal("composed resources or provenance lost", err)
	}
	if images[0].filename != "maps/floor.pdf" || !bytes.Equal(images[0].content, cm.BinaryData["floor-pdf"]) {
		t.Fatal("image reference/bytes changed")
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("caller snapshot mutated")
	}
	var root map[string]any
	json.Unmarshal(out, &root)
	floor := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
	if floor["image_path"] != "maps/floor.pdf" || len(floor["planned_access_points"].([]any)) != 1 {
		t.Fatal("design declarations discarded")
	}
	// Plain AP models need no image byte grant; image declarations do.
	plain := strings.Replace(cm.Data["model.yaml"], `"image_path":"maps/floor.pdf",`, "", 1)
	if _, m, e := ExportSiteDefaults([]byte(plain), nil, false); e != nil || m.PlannedAccessPoints != 1 || m.AssetsValidated {
		t.Fatal("pure AP default export failed", e)
	}
	if _, _, e := ExportSiteDefaults([]byte(cm.Data["model.yaml"]), nil, false); e == nil {
		t.Fatal("image source accepted without snapshot")
	}
	yamlRaw, err := yamlv3.Marshal(map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "data": cm.Data, "binaryData": map[string]string{"floor-pdf": "JVBERi0xLjcKZmlyc3Q="}})
	if err != nil {
		t.Fatal(err)
	}
	if _, m, e := ExportSiteDefaultsConfigMap(yamlRaw, "model.yaml", nil, false); e != nil || m.FloorImages != 1 {
		t.Fatal("YAML snapshot rejected", e)
	}
}
func TestSiteDefaultsDesignLogicalPathsAndNullImage(t *testing.T) {
	cm := siteDesignSnapshotFixture(t)
	logical := "/private/tmp/this-image-does-not-exist.pdf"
	cm.Data["model.yaml"] = strings.ReplaceAll(cm.Data["model.yaml"], "maps/floor.pdf", logical)
	cm.Data[floorImageAssetsKey] = strings.ReplaceAll(cm.Data[floorImageAssetsKey], "maps/floor.pdf", logical)
	raw, _ := json.Marshal(cm)
	if _, m, err := ExportSiteDefaultsConfigMap(raw, "model.yaml", nil, false); err != nil || m.FloorImages != 1 {
		t.Fatal("logical asset name was treated as a local file", err)
	}
	plain := strings.Replace(cm.Data["model.yaml"], fmt.Sprintf("\"image_path\":%q", logical), "\"image_path\":null", 1)
	out, m, err := ExportSiteDefaults([]byte(plain), nil, false)
	if err != nil || m.FloorImages != 0 || m.AssetsValidated || !bytes.Contains(out, []byte("\"image_path\": null")) {
		t.Fatal("pinned null image semantics lost", err)
	}
}

func TestSiteDefaultsDesignSnapshotRefusesPartialOrAmbiguousInput(t *testing.T) {
	for _, mode := range []string{"missing-index", "missing-byte", "corrupt-byte", "unused-corrupt-byte", "collision", "binary-index", "model-binary", "duplicate-index", "model-index", "unknown-snapshot-field", "wrong-kind", "missing-model", "invalid-model-key", "out-of-bounds", "bad-AP-field", "unknown-floor", "duplicate-document", "duplicate-key", "alias", "over-limit", "too-many-assets"} {
		t.Run(mode, func(t *testing.T) {
			cm := siteDesignSnapshotFixture(t)
			key := "model.yaml"
			switch mode {
			case "too-many-assets":
				index := map[string]string{}
				for i := 0; i < 65; i++ {
					name := fmt.Sprintf("%d.pdf", i)
					key := fmt.Sprintf("pdf-%d", i)
					index[name] = key
					cm.BinaryData[key] = []byte("%PDF-1.7\ntest")
				}
				encoded, _ := json.Marshal(index)
				cm.Data[floorImageAssetsKey] = string(encoded)
			case "missing-index":
				delete(cm.Data, floorImageAssetsKey)
			case "missing-byte":
				delete(cm.BinaryData, "floor-pdf")
			case "corrupt-byte":
				cm.BinaryData["floor-pdf"] = []byte("NEVER_PRINT_THIS")
			case "unused-corrupt-byte":
				cm.BinaryData["bad"] = []byte("NEVER_PRINT_THIS")
				cm.Data[floorImageAssetsKey] = `{"maps/floor.pdf":"floor-pdf","unused.png":"bad"}`
			case "collision":
				cm.Data["floor-pdf"] = "NEVER_PRINT_THIS"
			case "binary-index":
				cm.BinaryData[floorImageAssetsKey] = []byte("index")
			case "model-binary":
				cm.BinaryData[key] = []byte("model")
			case "duplicate-index":
				cm.Data[floorImageAssetsKey] = `{"maps/floor.pdf":"floor-pdf","maps/floor.pdf":"floor-pdf"}`
			case "model-index":
				key = floorImageAssetsKey
			case "wrong-kind":
				cm.Kind = "Secret"
			case "missing-model":
				delete(cm.Data, key)
			case "invalid-model-key":
				key = "../model"
			case "out-of-bounds":
				cm.Data[key] = strings.Replace(cm.Data[key], `"position_x":4.5`, `"position_x":31`, 1)
			case "bad-AP-field":
				cm.Data[key] = strings.Replace(cm.Data[key], `"position_x":4.5`, `"position_x":4.5,"NEVER_PRINT_THIS":true`, 1)
			case "unknown-floor":
				cm.Data[key] = strings.Replace(cm.Data[key], `"floor_number":1`, `"floor_number":1,"unknown":true`, 1)
			}
			raw, _ := json.Marshal(cm)
			switch mode {
			case "unknown-snapshot-field":
				raw = append(raw[:len(raw)-1], []byte(`,"unknown":"NEVER_PRINT_THIS"}`)...)
			case "duplicate-document":
				raw = append(raw, []byte("\n---\nkind: ConfigMap")...)
			case "duplicate-key":
				raw = []byte(`{"apiVersion":"v1","kind":"ConfigMap","data":{},"data":{"model.yaml":"NEVER_PRINT_THIS"}}`)
			case "alias":
				raw = []byte("apiVersion: v1\nkind: ConfigMap\ndata: &a {model.yaml: NEVER_PRINT_THIS}\nbinaryData: *a")
			case "over-limit":
				raw = []byte(strings.Repeat("x", (1<<20)+1))
			}
			out, _, err := ExportSiteDefaultsConfigMap(raw, key, nil, false)
			if err == nil || out != nil {
				t.Fatal("invalid snapshot exported")
			}
			if strings.Contains(err.Error(), "NEVER_PRINT_THIS") {
				t.Fatal("snapshot data disclosed")
			}
		})
	}
}

// Reuse this in public/native fixture sources. Only one known geometry leaf is
// omitted so existing explicit width/height edits retain their original intent.
func resolveSiteDesignDefaultFixture(t *testing.T, raw string, cm *corev1.ConfigMap) string {
	t.Helper()
	var root map[string]any
	if json.Unmarshal([]byte(raw), &root) != nil {
		t.Fatal("invalid test model")
	}
	floors := root["catalyst_center"].(map[string]any)["sites"].(map[string]any)["floors"].([]any)
	for _, v := range floors {
		delete(v.(map[string]any), "rf_model")
	}
	unresolved, _ := json.Marshal(root)
	var out []byte
	var meta SiteDefaultsProvenance
	var err error
	if cm == nil {
		out, meta, err = ExportSiteDefaults(unresolved, nil, false)
	} else {
		snapshot := cm.DeepCopy()
		snapshot.TypeMeta = metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"}
		key := "model.yaml"
		if _, present := snapshot.Data["model.json"]; present {
			key = "model.json"
		}
		snapshot.Data[key] = string(unresolved)
		snapshotBytes, _ := json.Marshal(snapshot)
		out, meta, err = ExportSiteDefaultsConfigMap(snapshotBytes, key, nil, false)
	}
	if err != nil || meta.DefaultedFields != len(floors) {
		t.Fatal("composed source did not resolve defaults", err)
	}
	var compact bytes.Buffer
	if json.Compact(&compact, out) != nil {
		t.Fatal("invalid resolved JSON")
	}
	return compact.String()
}
func TestSiteDefaultsDesignManifest(t *testing.T) {
	for _, mode := range []string{"defaults-recover-private", "image-defaults-recover-private", "image-defaults-keyed-recover-private"} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) { testPlannedAPManifestFloorThenPlacement(t, mode) })
	}
}
