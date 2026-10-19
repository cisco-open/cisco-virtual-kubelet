// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

func encodeSiteSelectionFixture(t *testing.T, raw []byte, opts nacmodel.SiteSelectionOptions) []byte {
	t.Helper()
	if opts.ManagedSites == nil {
		opts.ManagedSites = []string{}
	}
	out, err := json.Marshal(map[string]any{siteSelectionEnvelopeKey: siteSelectionDocument{Version: siteSelectionEnvelopeVersion, Model: raw, Selection: opts}})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func TestSiteSelectionSourceModes(t *testing.T) {
	for _, tc := range []struct {
		name  string
		opts  nacmodel.SiteSelectionOptions
		sites int
		aps   int
	}{
		{"all", nacmodel.SiteSelectionOptions{}, 4, 1},
		{"prefix", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}}, 4, 1},
		{"exact", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageSpecificSitesOnly: true}, 1, 0},
		{"floor", nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab/Building/Floor"}, ManageSpecificSitesOnly: true}, 1, 1},
		{"global-only", nacmodel.SiteSelectionOptions{ManageGlobalSettings: true}, 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"sites"}
			config.Spec.Mode = configapi.NetworkControllerApplyModeApply
			cm := siteSelectionSnapshotFixture(t)
			assets, err := loadFloorImageAssets(cm, "model.yaml")
			if err != nil {
				t.Fatal(err)
			}
			bound := encodeSiteSelectionFixture(t, []byte(cm.Data["model.yaml"]), tc.opts)
			if _, _, _, err := parseSitesWithFloorImages(bound, assets); err == nil {
				t.Fatal("older strict model parser would ignore selection")
			}
			config.Spec.Source.Inline = nil
			config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: "selection", Key: "model.yaml"}
			cm.ObjectMeta = metav1.ObjectMeta{Name: "selection", Namespace: config.Namespace, UID: "selection-uid"}
			cm.Data["model.yaml"] = string(bound)
			if err := a.statusWriter.Create(context.Background(), cm); err != nil {
				t.Fatal(err)
			}
			if err := a.statusWriter.Update(context.Background(), config); err != nil {
				t.Fatal(err)
			}
			source, err := loadControllerSource(context.Background(), a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			sites, aps, images, err := parseSitesWithFloorImages(source.raw, source.floorImages)
			if err != nil || len(sites) != tc.sites || len(aps) != tc.aps || len(images) != tc.aps || source.siteSelection == nil {
				t.Fatal("wrong execution projection", err)
			}
			original := source.siteSelection.clone()
			source.siteSelection.options.ManagedSites = append(source.siteSelection.options.ManagedSites, "Global/Other")
			if !reflect.DeepEqual(original.options, tc.opts) && !(len(original.options.ManagedSites) == 0 && len(tc.opts.ManagedSites) == 0) {
				t.Fatal("selection snapshot aliases mutable options")
			}
			// The selector and model share the backend version; changing either invalidates a submission snapshot.
			cm.Data["model.yaml"] = string(encodeSiteSelectionFixture(t, []byte(siteSelectionSnapshotFixture(t).Data["model.yaml"]), nacmodel.SiteSelectionOptions{ManageGlobalSettings: true}))
			if err := a.statusWriter.Update(context.Background(), cm); err != nil {
				t.Fatal(err)
			}
			if a.checkConfigurationSnapshot(context.Background(), config, source) == nil {
				t.Fatal("changed selector retained original source authority")
			}
		})
	}
}
func siteSelectionSnapshotFixture(t *testing.T) *corev1.ConfigMap {
	t.Helper()
	cm := floorImageAssetFixture()
	cm.TypeMeta = metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"}
	var model map[string]any
	json.Unmarshal([]byte(plannedAPFixture), &model)
	sites := model["catalyst_center"].(map[string]any)["sites"].(map[string]any)
	sites["areas"] = []any{map[string]any{"name": "Lab", "parent_name": "Global"}, map[string]any{"name": "Lab2", "parent_name": "Global"}}
	sites["buildings"] = []any{map[string]any{"name": "Building", "parent_name": "Global/Lab", "country": "United States"}}
	sites["floors"].([]any)[0].(map[string]any)["image_path"] = "maps/floor.pdf"
	b, _ := json.Marshal(model)
	cm.Data["model.yaml"] = string(b)
	return cm
}
func TestSiteSelectionPruneEligibility(t *testing.T) {
	declared := []string{"Global/Lab", "Global/Lab2", "Global/Other"}
	for _, tc := range []struct {
		opts nacmodel.SiteSelectionOptions
		path string
		want bool
	}{
		{nacmodel.SiteSelectionOptions{}, "Global/Old", true},
		{nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}}, "Global/Lab2/Old", true},
		{nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}}, "Global/Other/Old", false},
		{nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageSpecificSitesOnly: true}, "Global/Lab/Old", false},
		{nacmodel.SiteSelectionOptions{ManageGlobalSettings: true}, "Global/Old", false},
	} {
		chosen, err := nacmodel.ResolveSiteSelection(declared, tc.opts)
		if err != nil {
			t.Fatal(err)
		}
		scope := &siteSourceSelection{options: tc.opts, declared: declared, selected: chosen}
		got, err := scope.allows(tc.path)
		if err != nil || got != tc.want {
			t.Fatal("wrong omitted-target scope", err, tc.path)
		}
	}
}
func TestSiteSelectionManifestRetainsExcludedOwnership(t *testing.T) {
	for _, cm := range []bool{false, true} {
		t.Run(fmt.Sprintf("cm-%t", cm), func(t *testing.T) { exerciseSitePruneManifestKinds(t, "selection", cm, nil, true) })
	}
}
func TestSiteSelectionFullLifecycle(t *testing.T) {
	for _, cm := range []bool{false, true} {
		t.Run(fmt.Sprintf("cm-%t", cm), func(t *testing.T) {
			exerciseSitePruneManifestKinds(t, "complete", cm, nil, true, func(raw []byte) []byte { return encodeSiteSelectionFixture(t, raw, nacmodel.SiteSelectionOptions{}) })
		})
	}
}
func TestSiteSelectedExportRetainsFullSnapshot(t *testing.T) {
	cm := siteSelectionSnapshotFixture(t)
	raw, _ := json.Marshal(cm)
	opts := nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab/Building/Floor"}, ManageSpecificSitesOnly: true}
	out, meta, err := ExportSiteDefaultsBoundConfigMap(raw, "model.yaml", nil, false, &opts)
	if err != nil {
		t.Fatal(err)
	}
	if meta.Resources != 4 || meta.SiteSelection.SelectedSiteCount != 1 || meta.SelectedPlannedAccessPoints != 1 || meta.SelectedFloorImages != 1 || !meta.FullSourceRetained || meta.ArtifactFormat != "v1/ConfigMap" || meta.ModelOutputSHA256 == "" {
		t.Fatal("selection provenance lost")
	}
	var saved corev1.ConfigMap
	if json.Unmarshal(out, &saved) != nil {
		t.Fatal("invalid artifact")
	}
	doc, err := decodeSiteSelectionDocument([]byte(saved.Data["model.yaml"]))
	if err != nil {
		t.Fatal(err)
	}
	sites, _, _, err := parseSitesWithFloorImages(doc.Model, map[string][]byte{"maps/floor.pdf": cm.BinaryData["floor-pdf"]})
	if err != nil || len(sites) != 4 || !reflect.DeepEqual(saved.BinaryData, cm.BinaryData) || saved.Data[floorImageAssetsKey] != cm.Data[floorImageAssetsKey] {
		t.Fatal("excluded declarations or assets discarded", err)
	}
	again, _, err := ExportSiteDefaultsBoundConfigMap(out, "model.yaml", nil, false, nil)
	if err != nil || string(out) != string(again) {
		t.Fatal("re-export lost existing scope", err)
	}
	if _, _, err := ExportSiteDefaultsConfigMap(out, "model.yaml", nil, false); err == nil {
		t.Fatal("model-only export silently removed selection")
	}
	printed, _ := json.Marshal(meta)
	if strings.Contains(string(printed), "Global/Lab") {
		t.Fatal("private paths in provenance")
	}
}
func TestSiteSelectionSourceValidation(t *testing.T) {
	cm := siteSelectionSnapshotFixture(t)
	model := []byte(cm.Data["model.yaml"])
	for _, mode := range []string{"unknown-site", "exact-empty", "null-options", "null-list", "unknown-option", "wrong-version", "bad-excluded-model", "duplicate-root"} {
		t.Run(mode, func(t *testing.T) {
			opts := nacmodel.SiteSelectionOptions{ManageGlobalSettings: true}
			if mode == "unknown-site" {
				opts.ManagedSites = []string{"Global/La"}
			}
			if mode == "exact-empty" {
				opts.ManageSpecificSitesOnly = true
			}
			if mode == "bad-excluded-model" {
				model = []byte(strings.Replace(string(model), `"country":"United States"`, `"country":"United States","unknown":true`, 1))
			}
			raw := encodeSiteSelectionFixture(t, model, opts)
			switch mode {
			case "null-options":
				raw = []byte(`{"cvk_site_source":{"version":"sites-selection-v1","model":{},"selection":null}}`)
			case "null-list":
				raw = []byte(strings.Replace(string(raw), `"managed_sites":[]`, `"managed_sites":null`, 1))
			case "unknown-option":
				raw = []byte(strings.Replace(string(raw), `"managed_sites":[]`, `"managed_sites":[],"unknown":true`, 1))
			case "wrong-version":
				raw = []byte(strings.Replace(string(raw), "sites-selection-v1", "future", 1))
			case "duplicate-root":
				raw = []byte(`{"cvk_site_source":{},"cvk_site_source":{}}`)
			}
			config := &configapi.NetworkControllerConfig{Spec: configapi.NetworkControllerConfigSpec{ManagedSections: []string{"sites"}, Mode: configapi.NetworkControllerApplyModeApply, Source: configapi.NetworkControllerConfigurationSource{Inline: &runtime.RawExtension{Raw: raw}}}}
			source, err := loadControllerSource(context.Background(), nil, config)
			if err == nil {
				if _, _, _, compileErr := parseSitesWithFloorImages(source.raw, source.floorImages); compileErr == nil {
					t.Fatal("invalid selection accepted", mode)
				}
			}
		})
	}
}

func TestSiteSelectionDesignManifest(t *testing.T) {
	for _, mode := range []string{"selected-recover-private", "image-selected-keyed-recover-private"} {
		t.Run(mode, func(t *testing.T) { testPlannedAPManifestFloorThenPlacement(t, mode) })
	}
}
