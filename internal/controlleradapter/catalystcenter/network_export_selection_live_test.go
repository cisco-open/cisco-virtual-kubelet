//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

func TestLiveCatalystCenterNetworkSiteSelection(t *testing.T) {
	if os.Getenv("CATC_NETWORK_SITE_EXPORT") != "isolated-exact" || os.Getenv("CATC_NETWORK_SELECTION_QUALIFICATION") != "isolated-null-reselect" || os.Getenv("CATC_NETWORK_APPLICATION_QUALIFICATION") != "isolated-latest-applications" || os.Getenv("CATC_NETWORK_SUBMISSION_QUALIFICATION") != "isolated-public-preflight" || os.Getenv("CATC_NETWORK_TASK_RECOVERY") != "isolated-original-tasks" {
		t.Skip("isolated exact-site export qualification disabled")
	}
	qualifyLiveSiteExecution(t, true)
}

func liveExportSelectedNetworkModel(t *testing.T, raw []byte, root string) []byte {
	t.Helper()
	var document map[string]any
	if json.Unmarshal(raw, &document) != nil {
		t.Fatal("invalid site selection fixture")
	}
	model := document[nacmodel.Stripe].(map[string]any)
	sites := model["sites"].(map[string]any)
	refs := map[string]any{"network": "base", "telemetry": "base"}
	sites["global"] = map[string]any{"network_settings": refs}
	sites["areas"] = append(sites["areas"].([]any), map[string]any{"name": lastSiteName(root) + "-Excluded", "parent_name": "Global", "network_settings": refs})
	sites["buildings"] = []any{map[string]any{"name": "Excluded", "parent_name": root, "network_settings": refs}}
	input, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	out, meta, err := ExportSelectedNetworkSettings(input, nil, nacmodel.SiteSelectionOptions{ManagedSites: []string{root}, ManageSpecificSitesOnly: true})
	if err != nil || meta.SiteSelection == nil || meta.SiteSelection.SelectedSiteCount != 1 {
		t.Fatal("exact native fixture export failed", err)
	}
	bindings, err := parseNetworkSettings(out)
	if err != nil || len(bindings) != 1 || bindings[0].path != root {
		t.Fatal("unselected native targets escaped exporter", err)
	}
	t.Log("pinned exact-site export excluded Global, a sibling and a descendant before native planning; only the generated empty area's six settings remain")
	return out
}
