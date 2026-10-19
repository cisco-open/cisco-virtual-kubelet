// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
	"errors"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

const siteSelectionEnvelopeKey = "cvk_site_source"
const siteSelectionEnvelopeVersion = "sites-selection-v1"

type siteSelectionDocument struct {
	Version   string                        `json:"version"`
	Model     json.RawMessage               `json:"model"`
	Selection nacmodel.SiteSelectionOptions `json:"selection"`
}

// Pure source scope, not mutation authority. The complete model and this
// selection envelope share the existing ConfigMap UID/RV (or inline generation). Keep original source
// declarations available, including effects excluded from the execution view.
type siteSourceSelection struct {
	options  nacmodel.SiteSelectionOptions
	declared []string
	selected map[string]bool
}

func (s *siteSourceSelection) clone() *siteSourceSelection {
	if s == nil {
		return nil
	}
	copy := &siteSourceSelection{options: s.options, declared: append([]string(nil), s.declared...), selected: map[string]bool{}}
	copy.options.ManagedSites = append([]string{}, s.options.ManagedSites...)
	for k, v := range s.selected {
		copy.selected[k] = v
	}
	return copy
}

// An owned omitted target can still be intentionally pruned inside a selected
// prefix. Reuse the pinned selector for that path; do not treat excluded owned
// sites as deleted declarations, nor broaden exact-site mode to descendants.
func (s *siteSourceSelection) allows(path string) (bool, error) {
	if s == nil {
		return true, nil
	}
	if !validSitePath(path) || path == "Global" {
		return false, errConfigurationTransition
	}
	if chosen, known := s.selected[path]; known {
		return chosen, nil
	}
	declared := append(append([]string(nil), s.declared...), path)
	selected, err := nacmodel.ResolveSiteSelection(declared, s.options)
	return selected[path], err
}

func decodeSiteSelectionDocument(raw []byte) (siteSelectionDocument, error) {
	fail := func() (siteSelectionDocument, error) {
		return siteSelectionDocument{}, errors.New("invalid versioned site source")
	}
	node, err := nacmodel.ParseMappingDocument(raw)
	if err != nil || len(node.Content) != 2 || node.Content[0].Value != siteSelectionEnvelopeKey {
		return fail()
	}
	var object map[string]any
	if node.Decode(&object) != nil {
		return fail()
	}
	encoded, err := json.Marshal(object[siteSelectionEnvelopeKey])
	if err != nil {
		return fail()
	}
	var doc siteSelectionDocument
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&doc) != nil || doc.Version != siteSelectionEnvelopeVersion {
		return fail()
	}
	var fields map[string]json.RawMessage
	json.Unmarshal(encoded, &fields)
	if len(fields) != 3 || len(doc.Model) == 0 || bytes.Equal(bytes.TrimSpace(fields["selection"]), []byte("null")) {
		return fail()
	}
	var options map[string]json.RawMessage
	if json.Unmarshal(fields["selection"], &options) != nil || options == nil {
		return fail()
	}
	for _, v := range options {
		if bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
			return fail()
		}
	}
	if doc.Selection.ManagedSites == nil {
		doc.Selection.ManagedSites = []string{}
	}
	if _, err := nacmodel.ParseDocument(doc.Model); err != nil {
		return fail()
	}
	return doc, nil
}

func selectSiteSource(raw []byte, assets map[string][]byte, options nacmodel.SiteSelectionOptions, apply bool) ([]byte, *siteSourceSelection, error) {
	wants, aps, _, err := parseSitesWithFloorImages(raw, assets)
	if err != nil {
		return nil, nil, err
	}
	if apply && validateSiteExecution(wants) != nil {
		return nil, nil, errConfigurationTransition
	}
	if err := validateSiteExportPositions(wants, aps); err != nil {
		return nil, nil, err
	}
	declared := make([]string, 0, len(wants))
	for _, want := range wants {
		declared = append(declared, want.path)
	}
	chosen, err := nacmodel.ResolveSiteSelection(declared, options)
	if err != nil {
		return nil, nil, err
	}
	context := &siteSourceSelection{options: options, declared: declared, selected: map[string]bool{}}
	context.options.ManagedSites = append([]string{}, options.ManagedSites...)
	for _, path := range declared {
		context.selected[path] = chosen[path]
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, nil, err
	}
	var model map[string]any
	if node.Decode(&model) != nil {
		return nil, nil, errConfigurationTransition
	}
	sites := model["sites"].(map[string]any)
	for kind, value := range sites {
		rows := []any{}
		for _, entry := range value.([]any) {
			site := entry.(map[string]any)
			path := site["parent_name"].(string) + "/" + site["name"].(string)
			if chosen[path] {
				rows = append(rows, site)
			}
		}
		sites[kind] = rows
	}
	out, err := json.Marshal(map[string]any{nacmodel.Stripe: model})
	if err != nil {
		return nil, nil, err
	}
	return out, context, nil
}

func bindSiteSourceSelection(source *configSourceSnapshot, config *configapi.NetworkControllerConfig) error {
	if len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "sites" {
		return nil
	}
	node, err := nacmodel.ParseMappingDocument(source.raw)
	if err != nil {
		return nil
	} // ordinary compiler retains malformed-input handling
	envelope := false
	for i := 0; i < len(node.Content); i += 2 {
		envelope = envelope || node.Content[i].Value == siteSelectionEnvelopeKey
	}
	if !envelope {
		return nil
	}
	doc, err := decodeSiteSelectionDocument(source.raw)
	if err != nil {
		return err
	}
	selected, scope, err := selectSiteSource(doc.Model, source.floorImages, doc.Selection, config.Spec.Mode == configapi.NetworkControllerApplyModeApply)
	if err != nil {
		return err
	}
	source.raw, source.siteSelection = selected, scope
	return nil
}
