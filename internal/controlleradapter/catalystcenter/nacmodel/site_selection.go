// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import (
	_ "embed"
	"errors"
	"strings"
)

//go:embed site_selection.go
var siteSelectionPolicy []byte

var SiteSelectionPolicySHA256 = defaultsDigest(siteSelectionPolicy)

// SiteSelectionOptions corresponds to the pinned module's three selection
// variables. It selects source resources; it grants no native mutation authority.
type SiteSelectionOptions struct {
	ManagedSites            []string `json:"managed_sites"`
	ManageGlobalSettings    bool     `json:"manage_global_settings"`
	ManageSpecificSitesOnly bool     `json:"manage_specific_sites_only"`
}

// ResolveSiteSelection implements main.tf local.sites and the network-settings
// Global predicate. Callers supply canonical, fully validated non-Global paths.
// Prefix matching is deliberately literal startswith, as in the pinned module;
// it is not a path-segment descendant test (Lab also selects Lab2).
func ResolveSiteSelection(declared []string, options SiteSelectionOptions) (map[string]bool, error) {
	errSelection := errors.New("invalid or unresolved managed-site selection")
	if len(options.ManagedSites) > 1000 || options.ManageSpecificSitesOnly && len(options.ManagedSites) == 0 {
		return nil, errSelection
	}
	known := map[string]bool{}
	for _, path := range declared {
		if path == "Global" || !strings.HasPrefix(path, "Global/") || known[path] {
			return nil, errSelection
		}
		known[path] = true
	}
	managed := map[string]bool{}
	for _, path := range options.ManagedSites {
		// main.tf validates every explicitly requested site exists in YAML,
		// even when it would otherwise be a valid prefix or the Global root.
		if !known[path] {
			return nil, errSelection
		}
		managed[path] = true
	}
	selected := map[string]bool{}
	if options.ManageGlobalSettings || len(options.ManagedSites) == 0 {
		selected["Global"] = true
	}
	for _, path := range declared {
		if options.ManageSpecificSitesOnly {
			if managed[path] {
				selected[path] = true
			}
			continue
		}
		if len(options.ManagedSites) == 0 && !options.ManageGlobalSettings {
			selected[path] = true
		}
		for _, prefix := range options.ManagedSites {
			if strings.HasPrefix(path, prefix) {
				selected[path] = true
			}
		}
	}
	return selected, nil
}
