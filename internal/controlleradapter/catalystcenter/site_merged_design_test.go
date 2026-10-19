// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	"testing"
)

// Build the real design source from independent hierarchy/geometry and image/AP
// documents. Both declarations identify the same floor by name and parent, as
// utils yaml_merge requires; defaults and selection run after source assembly.
func mergeSiteDesignFixture(t *testing.T, raw []byte) []byte {
	t.Helper()
	var root map[string]any
	if json.Unmarshal(raw, &root) != nil {
		t.Fatal("invalid design merge input")
	}
	sites := root[nacmodel.Stripe].(map[string]any)["sites"].(map[string]any)
	var design []any
	for _, row := range sites["floors"].([]any) {
		floor := row.(map[string]any)
		effects := map[string]any{"name": floor["name"], "parent_name": floor["parent_name"]}
		for _, key := range []string{"image_path", "planned_access_points"} {
			if v, ok := floor[key]; ok {
				effects[key] = v
				delete(floor, key)
			}
		}
		design = append(design, effects)
	}
	base, _ := json.Marshal(root)
	overlay, _ := json.Marshal(map[string]any{nacmodel.Stripe: map[string]any{"sites": map[string]any{"floors": design}}})
	merged, meta, err := nacmodel.MergeModelSources([][]byte{base, overlay}, "model")
	if err != nil || len(meta.InputsSHA256) != 2 || meta.Resolved || meta.ConfigurationParity {
		t.Fatal("design source merge failed", err)
	}
	return merged
}

func TestMergedSiteDesignManifest(t *testing.T) {
	testPlannedAPManifestFloorThenPlacement(t, "merged-defaults-image-selected-keyed-recover-private")
}
