// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"slices"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
)

// Detect dependency context independently of submission options. Retained
// acknowledged work must still load the same assets after an option is removed.
func wiredBulkSourceHasSiteContext(config *configapi.NetworkControllerConfig, raw []byte) bool {
	if config == nil || len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory" {
		return false
	}
	node, err := nacmodel.ParseDocument(raw)
	if err != nil {
		return false
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return false
	}
	_, present := root["sites"]
	return present
}

// The source revision is the immutable dependency snapshot. Context does not
// acquire ownership or apply network settings; it is validated before use.
type wiredBulkSourceModel struct {
	inventory inventoryIntent
	states    map[string]string
	sites     []string
	network   *networkReportIntent
}

func parseWiredBulkSource(source *configSourceSnapshot) (inventoryIntent, map[string]string, []string, error) {
	model, err := compileWiredBulkSource(source)
	return model.inventory, model.states, model.sites, err
}

func compileWiredBulkSource(source *configSourceSnapshot) (wiredBulkSourceModel, error) {
	var model wiredBulkSourceModel
	if source == nil {
		return model, errConfigurationTransition
	}
	node, err := nacmodel.ParseDocument(source.raw)
	if err != nil {
		return model, err
	}
	var root map[string]any
	if node.Decode(&root) != nil {
		return model, errConfigurationTransition
	}
	for key := range root {
		if key != "inventory" && key != "sites" && key != "network_settings" {
			return model, errConfigurationTransition
		}
	}
	design, references, err := wiredBulkSiteProjections(root)
	if err != nil {
		return model, err
	}
	if design != nil {
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"sites": design}})
		if err != nil {
			return model, err
		}
		desired, _, _, err := parseSitesWithFloorImages(raw, source.floorImages)
		if err != nil {
			return model, err
		}
		model.sites = make([]string, 0, len(desired))
		for _, site := range desired {
			model.sites = append(model.sites, site.path)
		}
		slices.Sort(model.sites)
	}
	if network, present := root["network_settings"]; present {
		if design == nil {
			return model, errConfigurationTransition
		}
		raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": network, "sites": references}})
		if err != nil {
			return model, err
		}
		var credentials []globalCredentialIntent
		versioned := false
		if settings, ok := network.(map[string]any); ok {
			if definitions, present := settings["device_credentials"]; present && wiredBulkCredentialVersionsDeclared(definitions) {
				versioned = true
				// Preserve Apply-model version counters through the existing
				// credential splitter. This validates declarations only: no
				// secrets are resolved, applied or authenticated by this context.
				credentials, raw, err = splitNetworkCredentialConfiguration(raw)
				if err != nil {
					return model, err
				}
			}
		}
		intent, err := parseNetworkReportIntent(raw)
		if err != nil {
			return model, err
		}
		if versioned {
			intent.credentials = credentials
		}
		model.network = &intent
	} else if len(references) > 0 {
		return model, errConfigurationTransition
	}
	raw, err := json.Marshal(map[string]any{"catalyst_center": map[string]any{"inventory": root["inventory"]}})
	if err != nil {
		return model, err
	}
	model.inventory, model.states, err = parseWiredInventoryIntent(raw)
	if err != nil {
		return model, err
	}
	if model.sites != nil {
		for _, device := range model.inventory.devices {
			if !slices.Contains(model.sites, device.site) {
				return wiredBulkSourceModel{}, errConfigurationTransition
			}
		}
	}
	return model, nil
}

// Select the existing strict Apply parser for version-bearing declarations.
// Metadata-only import context retains the strict Report parser. A failed Apply
// parse never falls back; malformed fields/values are validated by that parser.
func wiredBulkCredentialVersionsDeclared(value any) bool {
	groups, _ := value.(map[string]any)
	for _, group := range groups {
		entries, _ := group.([]any)
		for _, entry := range entries {
			fields, _ := entry.(map[string]any)
			for key := range fields {
				if strings.HasSuffix(key, "_version") {
					return true
				}
			}
		}
	}
	return false
}

// Strip only independent site references from a private decoded tree. Full
// geometry/assets and profile/reference validation both run on their respective
// projections; neither path silently drops an unsupported field.
func wiredBulkSiteProjections(root map[string]any) (map[string]any, map[string]any, error) {
	value, present := root["sites"]
	if !present {
		return nil, nil, nil
	}
	sites, ok := value.(map[string]any)
	if !ok {
		return nil, nil, errConfigurationTransition
	}
	design, references := map[string]any{}, map[string]any{}
	for collection, value := range sites {
		if collection == "global" {
			references[collection] = value
			continue
		}
		if collection != "areas" && collection != "buildings" && collection != "floors" {
			return nil, nil, errConfigurationTransition
		}
		rows, ok := value.([]any)
		if !ok {
			return nil, nil, errConfigurationTransition
		}
		geometry, bindings := []any{}, []any{}
		for _, value := range rows {
			site, ok := value.(map[string]any)
			if !ok {
				return nil, nil, errConfigurationTransition
			}
			ref := map[string]any{"name": site["name"], "parent_name": site["parent_name"]}
			for key, value := range site {
				if key == "network_settings" || key == "ip_pools_reservations" || credentialSlot(key) != "" {
					ref[key] = value
					delete(site, key)
				}
			}
			if len(ref) > 2 {
				bindings = append(bindings, ref)
			}
			geometry = append(geometry, site)
		}
		design[collection] = geometry
		if len(bindings) > 0 {
			references[collection] = bindings
		}
	}
	return design, references, nil
}
