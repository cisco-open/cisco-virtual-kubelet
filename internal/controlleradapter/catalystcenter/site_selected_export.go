// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	_ "embed"
	"encoding/json"
	"errors"

	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

//go:embed site_selection_source.go
var siteSelectionSourcePolicy []byte

//go:embed site_selected_export.go
var siteSelectedExportPolicy []byte
var siteSelectedExportSHA = networkExportDigest(append(append([]byte(SiteDefaultsExporter+"\x00"+nacmodel.SiteSelectionPolicySHA256+"\x00"), siteSelectionSourcePolicy...), siteSelectedExportPolicy...))
var SiteSelectionExporter = "cvk-site-selected-v1:policy-sha256:" + siteSelectedExportSHA

// Emit one complete private ConfigMap artifact. The full resolved NetAsCode
// model stays inside a versioned source envelope, alongside its selection.
// Old parsers reject that envelope instead of executing an unfiltered model.
// Assets retain the existing same-ConfigMap index/BinaryData contract.
func ExportSiteDefaultsBoundConfigMap(raw []byte, modelKey string, overrides []byte, useBulkAPI bool, options *nacmodel.SiteSelectionOptions) ([]byte, SiteDefaultsProvenance, error) {
	fail := func() ([]byte, SiteDefaultsProvenance, error) {
		return nil, SiteDefaultsProvenance{}, errors.New("unsupported bound site source snapshot")
	}
	cm, assets, err := decodeSiteDefaultSnapshot(raw, modelKey)
	if err != nil {
		return fail()
	}
	model := []byte(cm.Data[modelKey])
	// Existing bound artifacts can be re-exported without losing their scope.
	if node, e := nacmodel.ParseMappingDocument(model); e == nil && len(node.Content) == 2 && node.Content[0].Value == siteSelectionEnvelopeKey {
		doc, e := decodeSiteSelectionDocument(model)
		if e != nil {
			return fail()
		}
		model = doc.Model
		if options == nil {
			options = &doc.Selection
		}
	}
	resolved, meta, err := exportSiteDefaults(model, overrides, useBulkAPI, assets, true)
	if err != nil {
		return fail()
	}
	meta.SourceSnapshotSHA256 = networkExportDigest(raw)
	meta.ModelOutputSHA256 = meta.OutputSHA256
	if options != nil {
		copied := *options
		copied.ManagedSites = append([]string{}, options.ManagedSites...)
		selected, scope, err := selectSiteSource(resolved, assets, copied, true)
		if err != nil {
			return fail()
		}
		wants, aps, images, err := parseSitesWithFloorImages(selected, assets)
		if err != nil {
			return fail()
		}
		doc := siteSelectionDocument{Version: siteSelectionEnvelopeVersion, Model: resolved, Selection: scope.options}
		bound, err := json.Marshal(map[string]any{siteSelectionEnvelopeKey: doc})
		if err != nil || len(bound) > 1<<20 {
			return fail()
		}
		cm.Data[modelKey] = string(bound)
		opts, _ := json.Marshal(scope.options)
		meta.SiteSelection = &nacmodel.NetworkSiteSelectionProvenance{OptionsSHA256: networkExportDigest(opts), SelectedSiteCount: len(wants)}
		meta.SelectedPlannedAccessPoints, meta.SelectedFloorImages = len(aps), len(images)
		meta.FullSourceRetained = true
		meta.Exporter, meta.PolicySHA256 = SiteSelectionExporter, siteSelectedExportSHA
		meta.ResolutionScope = "sites-defaults-and-managed-selection"
		meta.Limitations[0] = "One complete sites-only model with explicit managed-site selection; no file merging or other sections. Images require an explicit same-ConfigMap model/index/BinaryData snapshot; no model path is opened."
	} else {
		cm.Data[modelKey] = string(resolved)
	}
	out, err := json.MarshalIndent(cm, "", "  ")
	if err != nil || len(out)+1 > 1<<20 {
		return fail()
	}
	out = append(out, '\n')
	meta.ArtifactFormat = "v1/ConfigMap"
	meta.OutputSHA256 = networkExportDigest(out)
	meta.Limitations = append(meta.Limitations, "Selection changes execution scope only; the complete source remains bound to one revision. Native ownership, task recovery, retained effects and destructive lifecycle checks remain independent. Older workers refuse versioned selected envelopes.")
	return out, meta, nil
}
