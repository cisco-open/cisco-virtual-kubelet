// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const wiredBulkSitesFixture = `{"areas":[{"name":"Lab","parent_name":"Global"}],"buildings":[{"name":"Building","parent_name":"Global/Lab"}]}`

func wiredBulkSourceWithSites(t *testing.T, source *configSourceSnapshot, sites string) []byte {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(source.raw, &document); err != nil {
		t.Fatal(err)
	}
	var context any
	if err := json.Unmarshal([]byte(sites), &context); err != nil {
		t.Fatal(err)
	}
	document["catalyst_center"].(map[string]any)["sites"] = context
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestWiredBulkSiteAssetsUseExistingSourceRevision(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"inventory"}
	config.Annotations = map[string]string{wiredBulkAnnotation: "true"}
	raw := []byte(strings.Replace(plannedAPFixture, `"floor_number":1`, `"floor_number":1,"image_path":"floor.pdf"`, 1))
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	document["catalyst_center"].(map[string]any)["inventory"] = map[string]any{"devices": []any{map[string]any{"name": "a", "site": "Global/Lab/Building/Floor", "state": "PROVISION"}}}
	raw, _ = json.Marshal(document)
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: config.Namespace, Name: "bulk-model", UID: "bulk-model-uid"}, Data: map[string]string{"model.json": string(raw), floorImageAssetsKey: `{"floor.pdf":"floor-bytes"}`}, BinaryData: map[string][]byte{"floor-bytes": []byte("%PDF-1.7\nfirst")}}
	if err := a.statusWriter.Create(ctx, cm); err != nil {
		t.Fatal(err)
	}
	config.Spec.Source.Inline = nil
	config.Spec.Source.ConfigMapRef = &configapi.ConfigMapKeyRef{Name: cm.Name, Key: "model.json"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	source, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil || source.uid != cm.UID || source.version != cm.ResourceVersion || string(source.floorImages["floor.pdf"]) != "%PDF-1.7\nfirst" {
		t.Fatal("bulk context did not reuse source/asset snapshot", err)
	}
	if _, err := configuredWiredBulkBatches(config, source, nil); err != nil {
		t.Fatal("complete ConfigMap context did not compile", err)
	}
	cm.BinaryData["floor-bytes"] = []byte("%PDF-1.7\nsecond")
	if err := a.statusWriter.Update(ctx, cm); err != nil {
		t.Fatal(err)
	}
	if err := a.checkConfigurationSnapshot(ctx, config, source); !errors.Is(err, errConfigurationStale) || string(source.floorImages["floor.pdf"]) != "%PDF-1.7\nfirst" {
		t.Fatal("asset-only revision change did not fence original source", err)
	}
	fresh, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil || fresh.version == source.version || string(fresh.floorImages["floor.pdf"]) != "%PDF-1.7\nsecond" {
		t.Fatal("fresh source did not observe asset revision", err)
	}
	// An option change must not remove dependency bytes needed to recover an
	// acknowledged operation. Selection is not rerun during retained recovery.
	delete(config.Annotations, wiredBulkAnnotation)
	withoutOptions, err := loadControllerSource(ctx, a.statusReader, config)
	if err != nil || !reflect.DeepEqual(fresh.floorImages, withoutOptions.floorImages) {
		t.Fatal("option removal lost the recoverable asset context", err)
	}
}

func TestWiredBulkResolvedSiteContext(t *testing.T) {
	for _, mode := range []string{"source", "same-external", "extra-external", "missing-site", "empty-sites", "null-sites", "invalid-site-field", "unselected-section", "duplicate-yaml"} {
		t.Run(mode, func(t *testing.T) {
			_, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Annotations = map[string]string{wiredBulkAnnotation: "true", wiredBulkRootAnnotation: "Global/Lab"}
			source := &configSourceSnapshot{raw: []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab/Building","state":"PROVISION"}]}}}`)}
			sites := wiredBulkSitesFixture
			var external []string
			switch mode {
			case "same-external":
				external = []string{"Global/Lab/Building", "Global/Lab"}
			case "extra-external":
				external = []string{"Global", "Global/Lab", "Global/Lab/Building"}
			case "missing-site":
				sites = `{"areas":[{"name":"Lab","parent_name":"Global"}]}`
			case "empty-sites":
				sites = `{}`
			case "null-sites":
				sites = `null`
			case "invalid-site-field":
				sites = strings.Replace(sites, `"name":"Building"`, `"name":"Building","unsupported":true`, 1)
			}
			source.raw = wiredBulkSourceWithSites(t, source, sites)
			if mode == "unselected-section" {
				source.raw = []byte(strings.Replace(string(source.raw), `"catalyst_center":{`, `"catalyst_center":{"fabric":{},`, 1))
			}
			if mode == "duplicate-yaml" {
				source.raw = []byte("catalyst_center:\n  inventory: {}\n  sites: {}\n  sites: {}\n")
			}
			before := bytes.Clone(source.raw)
			batches, err := configuredWiredBulkBatches(config, source, external)
			valid := mode == "source" || mode == "same-external"
			if (err == nil) != valid || !bytes.Equal(source.raw, before) {
				t.Fatal("source context widened, ignored invalid declarations, or mutated source", err)
			}
			if valid && (len(batches) != 1 || batches[0].path != "Global/Lab" || len(batches[0].members) != 1) {
				t.Fatal("source-derived selection lost member")
			}
		})
	}
}

func TestWiredBulkContextRetainsFloorAssetValidation(t *testing.T) {
	for _, mode := range []string{"complete", "missing-asset", "bad-asset", "bad-ap", "bad-geometry"} {
		t.Run(mode, func(t *testing.T) {
			var document map[string]any
			if err := json.Unmarshal([]byte(plannedAPFixture), &document); err != nil {
				t.Fatal(err)
			}
			root := document["catalyst_center"].(map[string]any)
			root["inventory"] = map[string]any{"devices": []any{map[string]any{"name": "a", "site": "Global/Lab/Building/Floor", "state": "PROVISION"}}}
			floor := root["sites"].(map[string]any)["floors"].([]any)[0].(map[string]any)
			floor["image_path"] = "floor.pdf"
			assets := map[string][]byte{"floor.pdf": []byte("%PDF-1.7\nfixture")}
			switch mode {
			case "missing-asset":
				assets = nil
			case "bad-asset":
				assets["floor.pdf"] = []byte("invalid")
			case "bad-ap":
				floor["planned_access_points"] = []any{map[string]any{"name": "invalid"}}
			case "bad-geometry":
				floor["width"] = -1
			}
			raw, _ := json.Marshal(document)
			source := &configSourceSnapshot{raw: raw, floorImages: assets}
			before := bytes.Clone(raw)
			intent, states, paths, err := parseWiredBulkSource(source)
			if (err == nil) != (mode == "complete") || !bytes.Equal(before, source.raw) {
				t.Fatal("site dependency projection bypassed asset/placement/geometry validation", err)
			}
			if err == nil && (len(intent.devices) != 1 || states["a"] != "PROVISION" || !reflect.DeepEqual(paths, []string{"Global/Lab/Building/Floor"})) {
				t.Fatal("dependency context or inventory changed", paths)
			}
		})
	}
}

func TestWiredBulkPinnedSiteSelectionOptions(t *testing.T) {
	for _, mode := range []string{"subtree", "specific", "global-only", "global-managed", "specific-global", "root-outside-selection", "root-missing-managed", "specific-empty", "bad-specific", "bad-global", "disabled-selection"} {
		t.Run(mode, func(t *testing.T) {
			_, config := journalFixture(t)
			config.Spec.ManagedSections = []string{"inventory"}
			config.Annotations = map[string]string{wiredBulkAnnotation: "true", wiredBulkManagedSitesAnnotation: `["Global/Lab"]`}
			source := &configSourceSnapshot{raw: []byte(`{"catalyst_center":{"inventory":{"devices":[{"name":"a","site":"Global/Lab","state":"PROVISION"},{"name":"b","site":"Global/Lab/Building","state":"REPROVISION"}]}}}`)}
			source.raw = wiredBulkSourceWithSites(t, source, wiredBulkSitesFixture)
			switch mode {
			case "specific":
				config.Annotations[wiredBulkSpecificSitesAnnotation] = "true"
			case "global-only":
				delete(config.Annotations, wiredBulkManagedSitesAnnotation)
				config.Annotations[wiredBulkGlobalSettingsAnnotation] = "true"
			case "global-managed":
				config.Annotations[wiredBulkGlobalSettingsAnnotation] = "true"
			case "specific-global":
				config.Annotations[wiredBulkSpecificSitesAnnotation] = "true"
				config.Annotations[wiredBulkGlobalSettingsAnnotation] = "true"
			case "root-outside-selection":
				config.Annotations[wiredBulkManagedSitesAnnotation] = `["Global/Lab/Building"]`
				config.Annotations[wiredBulkRootAnnotation] = "Global/Lab"
			case "root-missing-managed":
				config.Annotations[wiredBulkManagedSitesAnnotation] = `["Global/Missing"]`
				config.Annotations[wiredBulkRootAnnotation] = "Global/Lab"
			case "specific-empty":
				delete(config.Annotations, wiredBulkManagedSitesAnnotation)
				config.Annotations[wiredBulkSpecificSitesAnnotation] = "true"
			case "bad-specific":
				config.Annotations[wiredBulkSpecificSitesAnnotation] = "yes"
			case "bad-global":
				config.Annotations[wiredBulkGlobalSettingsAnnotation] = ""
			case "disabled-selection":
				config.Annotations[wiredBulkAnnotation] = "false"
				config.Annotations[wiredBulkSpecificSitesAnnotation] = "false"
			}
			batches, err := configuredWiredBulkBatches(config, source, nil)
			valid := mode == "subtree" || mode == "specific" || mode == "global-only" || mode == "global-managed" || mode == "specific-global"
			if (err == nil) != valid {
				t.Fatal("module selection validation differs", err)
			}
			if !valid {
				return
			}
			names := []string{}
			for _, batch := range batches {
				for _, member := range batch.members {
					names = append(names, member.desired.reference.Name)
				}
			}
			want := []string{"a", "b"}
			if mode == "specific" || mode == "specific-global" {
				want = []string{"a"}
			}
			if mode == "global-only" {
				want = []string{}
			}
			if !reflect.DeepEqual(names, want) {
				t.Fatal("module selection widened or lost devices", names)
			}
		})
	}
}
