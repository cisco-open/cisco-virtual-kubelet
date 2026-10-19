// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

//go:embed site_defaults_export.go
var siteDefaultExportPolicy []byte

//go:embed sites.go
var siteDefaultParserPolicy []byte

//go:embed site_execution.go
var siteDefaultExecutionPolicy []byte
var siteDefaultExportSHA = networkExportDigest(append(append(append(append([]byte(nacmodel.SiteDefaultsPolicySHA256+"\x00"), siteDefaultExportPolicy...), siteDefaultParserPolicy...), siteDefaultExecutionPolicy...), siteDesignDefaultPolicy...))
var SiteDefaultsExporter = "cvk-site-defaults-v1:policy-sha256:" + siteDefaultExportSHA

type SiteDefaultsProvenance struct {
	nacmodel.DefaultsProvenance
	SiteExpressionsSHA256       string `json:"siteExpressionsSHA256"`
	UseBulkAPI                  bool   `json:"useBulkAPI"`
	Resources                   int    `json:"resources"`
	DefaultedFields             int    `json:"defaultedFields"`
	PlannedAccessPoints         int    `json:"plannedAccessPoints,omitempty"`
	FloorImages                 int    `json:"floorImages,omitempty"`
	AssetsValidated             bool   `json:"assetsValidated"`
	ImageAssets                 int    `json:"imageAssets,omitempty"`
	AssetSnapshotSHA256         string `json:"assetSnapshotSHA256,omitempty"`
	SourceSnapshotSHA256        string `json:"sourceSnapshotSHA256,omitempty"`
	ArtifactFormat              string `json:"artifactFormat,omitempty"`
	ModelOutputSHA256           string `json:"modelOutputSHA256,omitempty"`
	FullSourceRetained          bool   `json:"fullSourceRetained,omitempty"`
	SelectedPlannedAccessPoints int    `json:"selectedPlannedAccessPoints,omitempty"`
	SelectedFloorImages         int    `json:"selectedFloorImages,omitempty"`
}

// Explicit offline preprocessing only. Runtime parsers do not invoke this
// resolver, and native identities, hierarchy ownership and lifecycle authority
// remain the responsibility of the existing site executor.
func ExportSiteDefaults(raw, overrides []byte, useBulkAPI bool) ([]byte, SiteDefaultsProvenance, error) {
	return exportSiteDefaults(raw, overrides, useBulkAPI, nil, false)
}

func exportSiteDefaults(raw, overrides []byte, useBulkAPI bool, assets map[string][]byte, assetsValidated bool) ([]byte, SiteDefaultsProvenance, error) {
	fail := func() ([]byte, SiteDefaultsProvenance, error) {
		return nil, SiteDefaultsProvenance{}, errors.New("unsupported or unresolved site default input")
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return fail()
	}
	var model map[string]any
	if node.Decode(&model) != nil || len(model) != 1 {
		return fail()
	}
	sites, ok := model["sites"].(map[string]any)
	if !ok {
		return fail()
	}
	defaults, err := nacmodel.SiteDefaultsWithOverrides(overrides)
	if err != nil {
		return fail()
	}
	// Validate all overrides even when shadowed by explicit input or unused.
	for kind, fields := range defaults {
		probe := map[string]any{"name": "Validation", "parent_name": "Global"}
		if kind == "floors" {
			probe["parent_name"] = "Global/Validation"
			probe["floor_number"] = 0
		}
		for field, v := range fields {
			probe[field] = v
		}
		encoded, _ := json.Marshal(map[string]any{nacmodel.Stripe: map[string]any{"sites": map[string]any{kind: []any{probe}}}})
		desired, err := parseSites(encoded)
		if err != nil || validateSiteExecution(desired) != nil {
			return fail()
		}
	}
	count := 0
	for kind, value := range sites {
		fallback, known := defaults[kind]
		if !known {
			return fail()
		}
		rows, ok := value.([]any)
		if !ok {
			return fail()
		}
		for _, v := range rows {
			entry, ok := v.(map[string]any)
			if !ok {
				return fail()
			}
			if kind == "areas" {
				// Unlike the bulk expression, singular area for_each requires an
				// explicit parent. Never turn a filtered declaration into a new site.
				if _, present := entry["parent_name"]; !present && useBulkAPI {
					entry["parent_name"] = "Global"
					count++
				}
				continue
			}
			for field, fallbackValue := range fallback {
				v, present := entry[field]
				// Numeric multiplication inside upstream try() errors on null;
				// the fallback applies there. Non-numeric fields retain explicit null
				// and the existing strict parser refuses unexecutable resolved values.
				numeric := field == "width" || field == "length" || field == "height" || field == "latitude" || field == "longitude"
				if !present || numeric && v == nil {
					entry[field] = fallbackValue
					count++
				}
			}
		}
	}
	out, err := json.MarshalIndent(map[string]any{nacmodel.Stripe: model}, "", "  ")
	if err != nil || len(out)+1 > 1<<20 {
		return fail()
	}
	out = append(out, '\n')
	wants, placements, images, err := parseSitesWithFloorImages(out, assets)
	if err != nil || validateSiteExecution(wants) != nil || validateSiteExportPositions(wants, placements) != nil {
		return fail()
	}
	meta := SiteDefaultsProvenance{DefaultsProvenance: nacmodel.DefaultsProvenance{
		Exporter: SiteDefaultsExporter, ExecutionSyntaxValidated: true, Format: nacmodel.Format, ModelVersion: nacmodel.Version, UpstreamRevision: nacmodel.Revision,
		PolicySHA256: siteDefaultExportSHA, DefaultsSHA256: nacmodel.DefaultsSHA256, InputSHA256: networkExportDigest(raw), OutputSHA256: networkExportDigest(out), ResolutionScope: "sites-geometry-only",
		Limitations: []string{"One already-selected sites-only model; no file merging, managed-site filtering or other sections. Images require an explicit same-ConfigMap model/index/BinaryData snapshot; no model path is opened.", "Pinned site defaults and supported overrides only; numeric null fallback follows upstream try expressions, while unsupported unresolved values refuse.", "Singular area parents stay explicit; bulk omitted parent uses literal Global, matching the pinned resource map.", "No nested schema/parity attestation, native AP/antenna catalogue validation, API request, ownership transfer or change to runtime defaulting. Runtime revalidates source assets and native state independently."}},
		SiteExpressionsSHA256: nacmodel.SiteExpressionsSHA256, UseBulkAPI: useBulkAPI, Resources: len(wants), DefaultedFields: count, PlannedAccessPoints: len(placements), FloorImages: len(images), AssetsValidated: assetsValidated}
	if len(placements) > 0 || assetsValidated {
		meta.ResolutionScope = "sites-design-snapshot"
	}
	if assetsValidated {
		bound, err := json.Marshal(assets)
		if err != nil {
			return fail()
		}
		meta.ImageAssets = len(assets)
		meta.AssetSnapshotSHA256 = networkExportDigest(bound)
	}
	if len(overrides) > 0 {
		meta.OverridesSHA256 = networkExportDigest(overrides)
	}
	return out, meta, nil
}
