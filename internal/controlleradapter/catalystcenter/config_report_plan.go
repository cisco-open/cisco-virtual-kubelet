// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

type sectionReportPlan struct {
	name                  string
	count                 int
	note                  string
	observationNote       func() string
	incompleteObservation bool
	observe               func(context.Context) ([]configapi.NetworkControllerDriftEntry, error)
}

func reportSectionSupported(name string) bool {
	switch name {
	case "feature_templates", "wireless", "fabric", "sites", "network_settings", "authentication_templates", "templates", "inventory", "network_profiles", "system_settings":
		return true
	}
	return false
}

// Validate and compile every selected section before making the first API read.
// Projections discard only known cross-section reference fields whose owning
// section is also selected; unknown or unmanaged sections are never ignored.
func (c *client) compileConfigReports(raw []byte, sections []string, templateFiles ...map[string]string) ([]sectionReportPlan, error) {
	var files map[string]string
	if len(templateFiles) > 0 {
		files = templateFiles[0]
	}
	return c.compileConfigReportsWithAssets(raw, sections, files, nil)
}

func (c *client) compileConfigReportsWithAssets(raw []byte, sections []string, templateFiles map[string]string, floorImages map[string][]byte) ([]sectionReportPlan, error) {
	if len(sections) == 1 && sections[0] == "inventory" {
		if plan, err := c.compileFabricEWLCReport(raw); err == nil {
			return []sectionReportPlan{*plan}, nil
		}
	}
	// A handoff model includes inventory only as strictly validated fabric-device
	// prerequisites. Its dedicated compiler must consume the complete document.
	if len(sections) == 1 && sections[0] == "fabric" {
		if plan, err := c.compileFabricHandoffReport(raw); err == nil {
			return []sectionReportPlan{*plan}, nil
		}
		if plan, err := c.compileFabricEdgeReport(raw); err == nil {
			return []sectionReportPlan{*plan}, nil
		}
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return nil, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return nil, errors.New("invalid controller intent")
	}
	selected := map[string]bool{}
	for _, section := range sections {
		if !reportSectionSupported(section) || selected[section] {
			return nil, errors.New("unsupported or repeated section")
		}
		selected[section] = true
	}
	for section := range root {
		if !selected[section] && !(section == "sites" && selected["network_settings"]) {
			return nil, errors.New("source contains an unselected section")
		}
	}
	templatesShadowed := false
	if selected["templates"] && selected["inventory"] {
		templatesShadowed, err = normalizeReportTags(root)
		if err != nil {
			return nil, err
		}
	}
	plans := []sectionReportPlan{}
	for _, section := range sections {
		value, present := root[section]
		if !present {
			return nil, errors.New("selected section is absent from source")
		}
		if section == "templates" && templatesShadowed {
			plans = append(plans, sectionReportPlan{name: section, note: "tag definitions supplied by inventory", observe: func(context.Context) ([]configapi.NetworkControllerDriftEntry, error) { return nil, nil }})
			continue
		}
		projection := map[string]any{section: value}
		if section == "network_settings" {
			if sites, present := root["sites"]; present {
				projection["sites"] = sites
			}
		}
		// Clone before editing; one section's projection cannot change another's.
		encoded, _ := json.Marshal(projection)
		var copy map[string]any
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&copy); err != nil {
			return nil, errors.New("cannot project controller model")
		}
		if sites, ok := copy["sites"].(map[string]any); ok {
			for collection, value := range sites {
				if collection == "global" {
					if section == "sites" && selected["network_settings"] {
						global, ok := value.(map[string]any)
						if !ok || len(global) != 1 || global["network_settings"] == nil {
							return nil, errors.New("unsupported global site intent")
						}
						delete(sites, collection)
					}
					continue
				}
				list, ok := value.([]any)
				if !ok {
					return nil, errors.New("invalid site collection")
				}
				for _, entry := range list {
					object, ok := entry.(map[string]any)
					if !ok {
						return nil, errors.New("invalid site")
					}
					if section == "sites" && selected["network_settings"] {
						delete(object, "network_settings")
						delete(object, "ip_pools_reservations")
						for _, k := range globalCredentialKinds {
							delete(object, k.model)
						}
					}
					if section == "network_settings" && selected["sites"] {
						for _, field := range []string{"address", "country", "latitude", "longitude", "floor_number", "rf_model", "width", "length", "height", "units_of_measure", "image_path"} {
							delete(object, field)
						}
					}
				}
			}
		}
		encoded, _ = json.Marshal(map[string]any{"catalyst_center": copy})
		plan := sectionReportPlan{name: section}
		switch section {
		case "feature_templates":
			e, err := c.compileWirelessFeatureConfiguration(encoded)
			if err != nil {
				return nil, err
			}
			plan.count = e.count()
			plan.note = "Global FRA and CleanAir feature definitions; system templates protected"
			plan.observe = e.report
		case "wireless":
			e, err := c.compileWirelessConfiguration(encoded)
			if err != nil {
				return nil, err
			}
			plan.count = e.count()
			plan.note = "Global wireless interface, pre-auth ACL, power-profile and 802.11be definitions; no device deployment"
			plan.observe = e.report
		case "fabric":
			e, err := c.compileFabricConfiguration(encoded)
			if err != nil {
				return nil, err
			}
			if selected["network_settings"] {
				projection, err := networkIntentProjection(root)
				if err != nil {
					return nil, err
				}
				intent, err := parseNetworkReportIntent(projection)
				if err != nil {
					return nil, err
				}
				bindFabricIPAMPlanning(e, &ipamConfigurationExecutor{client: c, intent: intent})
			}
			plan.count = e.count()
			plan.note = "Global L3 virtual network and IP transit definitions; fabric memberships are retained"
			if _, extranet := e.configurationExecutor.(*fabricExtranetAdmissionGate); extranet {
				plan.note = "Read-only extranet policy drift; provider changes require replacement. Omitted or empty fabric_sites retain observed associations. Apply requires multi-device admission."
			}
			plan.observe = e.report
		case "system_settings":
			desired, err := parseSystemSettingsReport(encoded)
			if err != nil {
				return nil, err
			}
			plan.count = desired.count()
			if len(desired.servers) > 0 {
				plan.note = "AAA shared secrets and ISE passwords are not compared"
			}
			plan.observe = func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
				return c.reportSystemSettings(ctx, desired)
			}
		case "network_profiles":
			profiles, err := c.compileNetworkProfileConfiguration(encoded)
			if err != nil {
				return nil, err
			}
			plan.count = profiles.count()
			plan.observe = profiles.report
		case "inventory":
			desired, wiredStates, err := parseWiredInventoryIntent(encoded)
			if err != nil {
				desired, err = parseInventoryIntent(encoded)
				wiredStates = nil
			}
			if err != nil {
				return nil, err
			}
			for _, d := range desired.devices {
				if d.wireless != nil {
					return nil, errors.New("wireless provisioning report is not yet implemented")
				}
			}
			plan.count = len(desired.tags) + len(desired.devices) + len(desired.discoveries) + len(wiredStates)
			if len(wiredStates) > 0 {
				plan.note = "Wired provisioning observations are read-only. REPROVISION is explicit action intent; Report does not consume Apply receipts. Missing provisioning and site relocation require resolution before reprovisioning."
			}
			var outcomes *discoveryOutcomeSummary
			if len(desired.discoveries) > 0 {
				outcomes = &discoveryOutcomeSummary{}
				plan.observationNote = outcomes.message
				if len(wiredStates) > 0 {
					wiredNote := plan.note
					plan.observationNote = func() string { return wiredNote + " " + outcomes.message() }
				}
			}
			for _, d := range desired.devices {
				plan.count += len(d.tags)
				if d.role != "" {
					plan.count++
				}
			}
			plan.observe = func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
				if len(wiredStates) > 0 {
					return c.reportWiredInventoryIntent(ctx, desired, wiredStates, outcomes)
				}
				return c.reportInventoryIntent(ctx, desired, outcomes)
			}
		case "templates":
			desired, err := parseTemplateIntent(encoded, templateFiles)
			if err != nil {
				return nil, err
			}
			plan.count = len(desired.projects) + len(desired.definitions) + len(desired.tags) + (&templateMembershipExecutor{intent: desired}).count()
			plan.observe = func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
				return c.reportTemplateIntent(ctx, desired)
			}
		case "sites":
			desired, placements, images, err := parseSitesWithFloorImages(encoded, floorImages)
			if err != nil {
				return nil, err
			}
			plan.count = len(desired) + len(placements) + len(images)
			if len(images) > 0 {
				plan.incompleteObservation = true
				plan.note = "Floor image contents cannot be read back through the supported API; image drift remains unverified"
			}
			plan.observe = func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
				observed, err := c.ListSites(ctx)
				if err != nil {
					return nil, err
				}
				drift, err := planSites(desired, observed)
				if err != nil {
					return nil, err
				}
				positions, err := c.reportPlannedAPs(ctx, placements, observed)
				if err != nil {
					return nil, err
				}
				return append(drift, positions...), nil
			}
		case "network_settings":
			desired, err := parseNetworkReportIntent(encoded)
			if err != nil {
				return nil, err
			}
			plan.count = len(desired.pools) + len(desired.reservations) + len(desired.credentials) + len(desired.assignments)
			for _, binding := range desired.bindings {
				plan.count += len(binding.settings)
			}
			if len(desired.credentials) > 0 {
				plan.note = "Global credential values are not compared"
			}
			for _, b := range desired.bindings {
				for _, s := range b.settings {
					if s.kind == "aaa" {
						plan.note += "; AAA shared secrets are not compared"
					}
				}
			}
			plan.observe = func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
				var drift []configapi.NetworkControllerDriftEntry
				if len(desired.assignments) > 0 {
					e := &siteCredentialExecutor{client: c, desired: desired.assignments}
					d, err := e.report(ctx)
					if err != nil {
						return nil, err
					}
					drift = append(drift, d...)
				}
				if len(desired.credentials) > 0 {
					d, err := c.reportGlobalCredentials(ctx, desired.credentials)
					if err != nil {
						return nil, err
					}
					drift = append(drift, d...)
				}
				if len(desired.bindings) > 0 {
					d, err := c.reportNetworkSettings(ctx, desired.bindings)
					if err != nil {
						return nil, err
					}
					drift = append(drift, d...)
				}
				if len(desired.pools) > 0 {
					d, err := c.reportIPAM(ctx, desired)
					if err != nil {
						return nil, err
					}
					drift = append(drift, d...)
				}
				return drift, nil
			}
		case "authentication_templates":
			desired, err := parseAuthTemplates(encoded)
			if err != nil {
				return nil, err
			}
			plan.count = len(desired)
			plan.observe = func(ctx context.Context) ([]configapi.NetworkControllerDriftEntry, error) {
				return c.reportAuthTemplates(ctx, desired)
			}
		}
		plans = append(plans, plan)
	}
	return plans, nil
}
