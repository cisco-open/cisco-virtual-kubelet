// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

//go:embed network_export_selection.go
var networkSelectionPolicy []byte

func networkExportDigest(raw []byte) string {
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

var networkSelectionPolicySHA = networkExportDigest(append([]byte(nacmodel.NetworkDefaultsExporter+"\x00"+nacmodel.SiteSelectionPolicySHA256+"\x00"), networkSelectionPolicy...))
var NetworkSiteSelectionExporter = "cvk-network-selected-v1:policy-sha256:" + networkSelectionPolicySHA

// ExportSelectedNetworkSettings is offline preprocessing. Validate every
// declaration before selection so filtering cannot hide unsupported fields or
// unresolved references. The output then reuses the same runtime compiler.
func ExportSelectedNetworkSettings(raw, defaults []byte, options nacmodel.SiteSelectionOptions) ([]byte, nacmodel.NetworkDefaultsProvenance, error) {
	resolved, meta, err := nacmodel.ExportNetworkDefaults(raw, defaults)
	if err != nil {
		return nil, nacmodel.NetworkDefaultsProvenance{}, err
	}
	bindings, err := parseNetworkSettings(resolved)
	if err != nil {
		return nil, nacmodel.NetworkDefaultsProvenance{}, errInvalidResponse
	}
	declared := []string{}
	for _, binding := range bindings {
		if binding.path != "Global" {
			declared = append(declared, binding.path)
		}
	}
	selected, err := nacmodel.ResolveSiteSelection(declared, options)
	if err != nil {
		return nil, nacmodel.NetworkDefaultsProvenance{}, err
	}
	var root map[string]any
	if json.Unmarshal(resolved, &root) != nil {
		return nil, nacmodel.NetworkDefaultsProvenance{}, errInvalidResponse
	}
	model := root[nacmodel.Stripe].(map[string]any)
	sites := model["sites"].(map[string]any)
	used := map[string]map[string]bool{}
	count := 0
	retain := func(site map[string]any, path string) bool {
		if !selected[path] {
			return false
		}
		count++
		for category, name := range site["network_settings"].(map[string]any) {
			if used[category] == nil {
				used[category] = map[string]bool{}
			}
			used[category][name.(string)] = true
		}
		return true
	}
	for category, value := range sites {
		if category == "global" {
			if !retain(value.(map[string]any), "Global") {
				delete(sites, category)
			}
			continue
		}
		kept := []any{}
		for _, value := range value.([]any) {
			site := value.(map[string]any)
			if retain(site, site["parent_name"].(string)+"/"+site["name"].(string)) {
				kept = append(kept, site)
			}
		}
		if len(kept) == 0 {
			delete(sites, category)
		} else {
			sites[category] = kept
		}
	}
	profiles := model["network_settings"].(map[string]any)
	for category, value := range profiles {
		kept := []any{}
		for _, value := range value.([]any) {
			if used[category][value.(map[string]any)["name"].(string)] {
				kept = append(kept, value)
			}
		}
		if len(kept) == 0 {
			delete(profiles, category)
		} else {
			profiles[category] = kept
		}
	}
	out, err := json.MarshalIndent(root, "", "  ")
	if err != nil || len(out)+1 > 1<<20 {
		return nil, nacmodel.NetworkDefaultsProvenance{}, errInvalidResponse
	}
	out = append(out, '\n')
	if ValidateNetworkSettingsSource(out) != nil {
		return nil, nacmodel.NetworkDefaultsProvenance{}, errInvalidResponse
	}
	encodedOptions, _ := json.Marshal(options)
	meta.Exporter, meta.PolicySHA256 = NetworkSiteSelectionExporter, networkSelectionPolicySHA
	meta.ExecutionSyntaxValidated = true
	meta.OutputSHA256 = networkExportDigest(out)
	meta.ResolutionScope = "network-settings-profiles-and-site-selection"
	meta.SiteSelection = &nacmodel.NetworkSiteSelectionProvenance{OptionsSHA256: networkExportDigest(encodedOptions), SelectedSiteCount: count}
	meta.Limitations = []string{"Not full nested schema validation or complete model/default parity.", "No multi-file merging, native identity resolution, ownership transfer or API execution.", "Explicit null collector selection and null default overrides require upstream resolution.", "Selection applies only to validated network/telemetry profile bindings, not other module sections; literal upstream prefix semantics apply."}
	return out, meta, nil
}
