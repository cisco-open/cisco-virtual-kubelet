// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

//go:embed discovery_export_selection.go
var discoverySelectionPolicy []byte

//go:embed discovery.go
var discoverySelectionParser []byte

//go:embed sites.go
var discoverySelectionSitesParser []byte

var discoverySelectionPolicySHA = networkExportDigest(append(append(append([]byte(nacmodel.SiteSelectionPolicySHA256+"\x00"), discoverySelectionPolicy...), discoverySelectionParser...), discoverySelectionSitesParser...))
var DiscoverySiteSelectionExporter = "cvk-discovery-selected-v1:policy-sha256:" + discoverySelectionPolicySHA

type DiscoverySelectionProvenance struct {
	DefaultsSHA256           string   `json:"defaultsSHA256,omitempty"`
	TerraformSHA256          string   `json:"terraformSHA256,omitempty"`
	OverridesSHA256          string   `json:"overridesSHA256,omitempty"`
	AppliedDefaultFields     int      `json:"appliedDefaultFields,omitempty"`
	Exporter                 string   `json:"exporter"`
	Format                   string   `json:"format"`
	ModelVersion             string   `json:"modelVersion"`
	UpstreamRevision         string   `json:"upstreamRevision"`
	PolicySHA256             string   `json:"policySHA256"`
	InputSHA256              string   `json:"inputSHA256"`
	OutputSHA256             string   `json:"outputSHA256"`
	OptionsSHA256            string   `json:"optionsSHA256"`
	DeclaredJobs             int      `json:"declaredJobs"`
	SelectedJobs             int      `json:"selectedJobs"`
	PerSiteMode              bool     `json:"perSiteMode"`
	ExecutionSyntaxValidated bool     `json:"executionSyntaxValidated"`
	LeafSchemaValidated      bool     `json:"leafSchemaValidated"`
	ConfigurationParity      bool     `json:"configurationParity"`
	Limitations              []string `json:"limitations"`
}

// ExportSelectedDiscovery applies cc_discovery.tf's for_each predicate offline.
// create_per_site never changes a job's global native name or assigns devices
// to a site. All jobs are validated before filtering; unrelated model sections
// are refused instead of silently discarded. Inputs are already leaf-resolved.
func ExportSelectedDiscovery(raw []byte, options nacmodel.SiteSelectionOptions) ([]byte, DiscoverySelectionProvenance, error) {
	fail := func() ([]byte, DiscoverySelectionProvenance, error) {
		return nil, DiscoverySelectionProvenance{}, errors.New("unsupported or unresolved discovery selection input")
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
		if k != "sites" && k != "inventory" {
			return fail()
		}
	}
	inventory, ok := model["inventory"].(map[string]any)
	if !ok || len(inventory) != 1 {
		return fail()
	}
	rows, ok := inventory["discovery"].([]any)
	if !ok {
		return fail()
	}
	declared := []string{}
	if sites, present := model["sites"]; present {
		encoded, err := json.Marshal(map[string]any{nacmodel.Stripe: map[string]any{"sites": sites}})
		if err != nil {
			return fail()
		}
		parsed, err := parseSites(encoded)
		if err != nil {
			return fail()
		}
		for _, site := range parsed {
			declared = append(declared, site.path)
		}
	}
	if _, err := nacmodel.ResolveSiteSelection(declared, options); err != nil {
		return fail()
	}
	stripped := make([]any, 0, len(rows))
	flags := make([]bool, 0, len(rows))
	for _, row := range rows {
		m, ok := row.(map[string]any)
		if !ok {
			return fail()
		}
		copy := make(map[string]any, len(m))
		perSite := false
		for k, v := range m {
			if k == "create_per_site" {
				b, ok := v.(bool)
				if !ok {
					return fail()
				}
				perSite = b
			} else {
				copy[k] = v
			}
		}
		stripped = append(stripped, copy)
		flags = append(flags, perSite)
	}
	if _, err := parseDiscoveryModels(stripped); err != nil {
		return fail()
	}
	selected := make([]any, 0, len(rows))
	perSiteMode := !options.ManageGlobalSettings && len(options.ManagedSites) != 0
	for i, row := range stripped {
		if !perSiteMode || flags[i] {
			selected = append(selected, row)
		}
	}
	output, err := json.MarshalIndent(map[string]any{nacmodel.Stripe: map[string]any{"inventory": map[string]any{"discovery": selected}}}, "", "  ")
	if err != nil || len(output)+1 > 1<<20 {
		return fail()
	}
	output = append(output, '\n')
	// The exact output enters the ordinary runtime compiler; no alternate sender.
	if _, err := parseInventoryIntent(output); err != nil {
		return fail()
	}
	opts, _ := json.Marshal(options)
	meta := DiscoverySelectionProvenance{Exporter: DiscoverySiteSelectionExporter, Format: nacmodel.Format, ModelVersion: nacmodel.Version, UpstreamRevision: nacmodel.Revision, PolicySHA256: discoverySelectionPolicySHA, InputSHA256: networkExportDigest(raw), OutputSHA256: networkExportDigest(output), OptionsSHA256: networkExportDigest(opts), DeclaredJobs: len(rows), SelectedJobs: len(selected), PerSiteMode: perSiteMode, ExecutionSyntaxValidated: true, Limitations: []string{"Selection only: fields/defaults must already be resolved; no multi-file merging or full nested schema validation.", "create_per_site is deployment selection, not native site assignment; discovery names remain globally unique.", "Site declarations validate selection and are not emitted as site mutation intent; credentials resolve through existing named native lookups.", "No ownership adoption, API execution or automatic rerun is authorized by this export; runtime retention and physical safeguards still apply.", "Inline credentials and other model sections remain unsupported by this exporter; full pinned-model parity is not claimed."}}
	return output, meta, nil
}
