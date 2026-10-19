// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"slices"
	"strings"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

// Module-level transport/grouping options are kept outside the resolved model,
// following the existing bulk-sites annotation convention. These options do
// not grant physical mutation authority or enable the incomplete public cycle.
const (
	wiredBulkAnnotation               = "cisco.vk/catalyst-center-bulk-devices"
	wiredBulkRootAnnotation           = "cisco.vk/catalyst-center-bulk-site-provisioning"
	wiredBulkManagedSitesAnnotation   = "cisco.vk/catalyst-center-managed-sites"
	wiredBulkSpecificSitesAnnotation  = "cisco.vk/catalyst-center-bulk-manage-specific-sites-only"
	wiredBulkGlobalSettingsAnnotation = "cisco.vk/catalyst-center-bulk-manage-global-settings"
)

func wiredBulkOptions(config *configapi.NetworkControllerConfig) (bool, wiredBulkGrouping, error) {
	var options wiredBulkGrouping
	if config == nil {
		return false, options, errConfigurationTransition
	}
	flag := config.Annotations[wiredBulkAnnotation]
	if flag != "" && flag != "true" && flag != "false" {
		return false, options, errConfigurationTransition
	}
	enabled := flag == "true"
	for key, target := range map[string]*bool{
		wiredBulkSpecificSitesAnnotation:  &options.specificSitesOnly,
		wiredBulkGlobalSettingsAnnotation: &options.globalSettings,
	} {
		if value, exists := config.Annotations[key]; exists {
			if !enabled || value != "true" && value != "false" {
				return false, wiredBulkGrouping{}, errConfigurationTransition
			}
			*target = value == "true"
		}
	}
	options.root = config.Annotations[wiredBulkRootAnnotation]
	if options.root != "" && (!enabled || !validSitePath(options.root) || strings.Count(options.root, "/") > 4) {
		return false, wiredBulkGrouping{}, errConfigurationTransition
	}
	if raw, exists := config.Annotations[wiredBulkManagedSitesAnnotation]; exists {
		if !enabled || len(raw) > 65536 || json.Unmarshal([]byte(raw), &options.managedSites) != nil || options.managedSites == nil || len(options.managedSites) > 1000 {
			return false, wiredBulkGrouping{}, errConfigurationTransition
		}
		seen := map[string]bool{}
		for _, path := range options.managedSites {
			if !validSitePath(path) || seen[path] {
				return false, wiredBulkGrouping{}, errConfigurationTransition
			}
			seen[path] = true
		}
	}
	if options.specificSitesOnly && len(options.managedSites) == 0 {
		return false, wiredBulkGrouping{}, errConfigurationTransition
	}
	if enabled && (len(config.Spec.ManagedSections) != 1 || config.Spec.ManagedSections[0] != "inventory") {
		return false, wiredBulkGrouping{}, errConfigurationTransition
	}
	return enabled, options, nil
}

// Prefer the complete resolved source's site context. Inventory-only private
// callers must still supply their resolved site set explicitly. A second set
// cannot widen or replace declarations embedded in the actual pinned source.
func configuredWiredBulkBatches(config *configapi.NetworkControllerConfig, source *configSourceSnapshot, declaredSites []string) ([]wiredBulkBatch, error) {
	enabled, options, err := wiredBulkOptions(config)
	if err != nil {
		return nil, err
	}
	if !enabled || source == nil {
		return nil, errConfigurationTransition
	}
	intent, states, modelSites, err := parseWiredBulkSource(source)
	if err != nil {
		return nil, err
	}
	if modelSites != nil {
		if declaredSites != nil {
			external := slices.Clone(declaredSites)
			slices.Sort(external)
			if !slices.Equal(external, modelSites) {
				return nil, errConfigurationStale
			}
		}
		declaredSites = modelSites
	}
	return groupWiredBulkMembers(intent, states, declaredSites, options)
}
