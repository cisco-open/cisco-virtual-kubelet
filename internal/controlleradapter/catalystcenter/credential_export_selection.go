// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

//go:embed credential_export_selection.go
var credentialSelectionPolicy []byte

//go:embed ip_pools.go
var credentialSelectionReferenceParser []byte
var credentialSelectionPolicySHA = networkExportDigest(append(append([]byte(CredentialDefaultsExporter+"\x00"+nacmodel.SiteSelectionPolicySHA256+"\x00"), credentialSelectionPolicy...), credentialSelectionReferenceParser...))
var CredentialSiteSelectionExporter = "cvk-credential-selected-v1:policy-sha256:" + credentialSelectionPolicySHA

type CredentialSiteSelectionProvenance struct {
	OptionsSHA256       string `json:"optionsSHA256"`
	DeclaredDefinitions int    `json:"declaredDefinitions"`
	SelectedDefinitions int    `json:"selectedDefinitions"`
	SelectedAssignments int    `json:"selectedAssignments"`
	SelectedSites       int    `json:"selectedSites"`
	PerSiteMode         bool   `json:"perSiteMode"`
}

// ExportSelectedCredentials applies the pinned credential-definition and site
// assignment predicates offline. Site declarations are references, not geometry
// or creation intent. All declarations are validated before selection.
func ExportSelectedCredentials(raw, overrides []byte, options nacmodel.SiteSelectionOptions) ([]byte, CredentialDefaultsProvenance, error) {
	fail := func() ([]byte, CredentialDefaultsProvenance, error) {
		return nil, CredentialDefaultsProvenance{}, errors.New("unsupported or unresolved credential selection input")
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return fail()
	}
	var model map[string]any
	if node.Decode(&model) != nil {
		return fail()
	}
	for k := range model {
		if k != "sites" && k != "network_settings" {
			return fail()
		}
	}
	network := map[string]any{}
	if value, present := model["network_settings"]; present {
		var ok bool
		network, ok = value.(map[string]any)
		if !ok {
			return fail()
		}
	}
	for k := range network {
		if k != "device_credentials" {
			return fail()
		}
	}
	definitions := any(map[string]any{})
	if v, present := network["device_credentials"]; present {
		definitions = v
	}
	projection, _ := json.Marshal(map[string]any{nacmodel.Stripe: map[string]any{"network_settings": map[string]any{"device_credentials": definitions}}})
	resolved, meta, err := ExportCredentialDefaultsWithOverrides(projection, overrides)
	if err != nil {
		return fail()
	}
	var resolvedRoot map[string]any
	if json.Unmarshal(resolved, &resolvedRoot) != nil {
		return fail()
	}
	resolvedNetwork := resolvedRoot[nacmodel.Stripe].(map[string]any)["network_settings"].(map[string]any)
	sites := map[string]any{}
	if v, present := model["sites"]; present {
		var ok bool
		sites, ok = v.(map[string]any)
		if !ok {
			return fail()
		}
	}
	// Reject unrelated declarations even if they would be filtered out. The
	// runtime reference parser supplies path, duplicate and assignment validation.
	for category, value := range sites {
		if category != "areas" && category != "buildings" && category != "floors" {
			return fail()
		}
		entries, ok := value.([]any)
		if !ok {
			return fail()
		}
		for _, entry := range entries {
			m, ok := entry.(map[string]any)
			if !ok {
				return fail()
			}
			for k := range m {
				if k != "name" && k != "parent_name" && credentialSlot(k) == "" {
					return fail()
				}
			}
		}
	}
	references, _ := json.Marshal(map[string]any{nacmodel.Stripe: map[string]any{"network_settings": map[string]any{}, "sites": sites}})
	intent, err := parseNetworkReportIntent(references)
	if err != nil {
		return fail()
	}
	for _, a := range intent.assignments {
		if a.name == "" {
			return fail()
		}
	}
	pathOf := func(category string, m map[string]any) string {
		name, _ := m["name"].(string)
		parent, _ := m["parent_name"].(string)
		if category == "areas" && name == "Global" {
			if _, present := m["parent_name"]; !present {
				return "Global"
			}
		}
		return parent + "/" + name
	}
	declared := []string{}
	for category, value := range sites {
		for _, entry := range value.([]any) {
			path := pathOf(category, entry.(map[string]any))
			if path != "Global" {
				declared = append(declared, path)
			}
		}
	}
	selected, err := nacmodel.ResolveSiteSelection(declared, options)
	if err != nil {
		return fail()
	}
	kept := map[string]any{}
	siteCount := 0
	for category, value := range sites {
		rows := []any{}
		for _, entry := range value.([]any) {
			if selected[pathOf(category, entry.(map[string]any))] {
				rows = append(rows, entry)
				siteCount++
			}
		}
		if len(rows) > 0 {
			kept[category] = rows
		}
	}
	global := options.ManageGlobalSettings || len(options.ManagedSites) == 0
	outputNetwork := map[string]any{}
	selectedDefinitions := 0
	if global {
		outputNetwork = resolvedNetwork
		selectedDefinitions = meta.Resources
	}
	outputModel := map[string]any{"network_settings": outputNetwork}
	if len(kept) > 0 {
		outputModel["sites"] = kept
	}
	out, err := json.MarshalIndent(map[string]any{nacmodel.Stripe: outputModel}, "", "  ")
	if err != nil || len(out)+1 > 1<<20 {
		return fail()
	}
	out = append(out, '\n')
	// Validate selected named references through the ordinary compiler. Global
	// definitions retain their separate version-bound adapter-owned writer.
	check := out
	if global {
		_, remaining, err := splitNetworkCredentialConfiguration(out)
		if err != nil {
			return fail()
		}
		check = remaining
	}
	assignments := 0
	if check != nil {
		parsed, err := parseNetworkReportIntent(check)
		if err != nil {
			return fail()
		}
		assignments = len(parsed.assignments)
		if _, err := (&client{}).compileConfigurationExecutor(check, "network_settings"); err != nil {
			return fail()
		}
	}
	opts, _ := json.Marshal(options)
	meta.SiteSelection = &CredentialSiteSelectionProvenance{OptionsSHA256: networkExportDigest(opts), DeclaredDefinitions: meta.Resources, SelectedDefinitions: selectedDefinitions, SelectedAssignments: assignments, SelectedSites: siteCount, PerSiteMode: !global}
	meta.Resources = selectedDefinitions + assignments
	meta.Exporter = CredentialSiteSelectionExporter
	meta.PolicySHA256 = credentialSelectionPolicySHA
	meta.InputSHA256 = networkExportDigest(raw)
	meta.OutputSHA256 = networkExportDigest(out)
	meta.Limitations = []string{"Credential definitions and minimal site-assignment references only; no site creation/geometry, other sections or file merging.", "All normalized definitions and references are validated before selection; explicit null assignment removal is unsupported.", "Per-site output omits credential creation and uses the existing exact named native lookup; unused Secret references must not be carried into the per-site config.", "No implicit Global-slot fallback, native identity adoption, ownership transfer, API operation or full schema/parity attestation is performed."}
	return out, meta, nil
}
