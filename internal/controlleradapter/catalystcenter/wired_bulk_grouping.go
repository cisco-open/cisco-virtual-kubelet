// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"sort"
	"strings"
)

type wiredBulkGrouping struct {
	root              string
	managedSites      []string
	specificSitesOnly bool
	globalSettings    bool
}
type wiredBulkBatch struct {
	path    string
	members []wiredBulkMember
}

func siteWithin(path, root string) bool { return path == root || strings.HasPrefix(path, root+"/") }

// Group only explicitly selected wired provisioning declarations. The caller
// supplies the resolved model's site set; native site existence is separately
// checked during preflight. No transport/module options are inserted into the
// NetAsCode stripe, and unsupported embedded/wireless members remain refused.
func groupWiredBulkMembers(intent inventoryIntent, states map[string]string, declaredSites []string, options wiredBulkGrouping) ([]wiredBulkBatch, error) {
	sites := map[string]bool{}
	for _, path := range declaredSites {
		if !validSitePath(path) || sites[path] {
			return nil, errConfigurationTransition
		}
		sites[path] = true
	}
	if options.root != "" {
		if !validSitePath(options.root) || strings.Count(options.root, "/") > 4 || !sites[options.root] {
			return nil, errConfigurationTransition
		}
	}
	managed := append([]string(nil), options.managedSites...)
	sort.Strings(managed)
	if options.specificSitesOnly && len(managed) == 0 {
		return nil, errConfigurationTransition
	}
	for i, path := range managed {
		if !validSitePath(path) || !sites[path] {
			return nil, errConfigurationTransition
		}
		for j := 0; j < i; j++ {
			if path == managed[j] {
				return nil, errConfigurationTransition
			}
			if options.root == "" && (siteWithin(path, managed[j]) || siteWithin(managed[j], path)) {
				return nil, errConfigurationOwned
			}
		}
	}
	selectedSite := func(path string) bool {
		for _, scope := range managed {
			if path == scope || !options.specificSitesOnly && siteWithin(path, scope) {
				return true
			}
		}
		return len(managed) == 0 && !options.globalSettings
	}
	if options.root != "" && !selectedSite(options.root) {
		return nil, errConfigurationTransition
	}
	// An explicit bulk root takes precedence over managed-site grouping upstream;
	// managed grouping is not evaluated when an explicit bulk root is selected.
	type key struct{ path, state string }
	groups := map[key][]wiredBulkMember{}
	names := map[string]bool{}
	for _, device := range intent.devices {
		name := device.reference.Name
		if name == "" || names[name] || !validSitePath(device.site) || !sites[device.site] {
			return nil, errConfigurationTransition
		}
		names[name] = true
		state, selected := states[name]
		if !selected {
			continue
		}
		if state != "PROVISION" && state != "REPROVISION" || device.wireless != nil {
			return nil, errConfigurationTransition
		}
		if !selectedSite(device.site) {
			continue
		}
		root := device.site
		if options.root != "" {
			if !siteWithin(device.site, options.root) {
				continue
			}
			root = options.root
		} else if len(managed) > 0 {
			root = ""
			for _, scope := range managed {
				if siteWithin(device.site, scope) {
					if root != "" {
						return nil, errConfigurationOwned
					}
					root = scope
				}
			}
			if root == "" {
				continue
			}
		}
		k := key{root, state}
		groups[k] = append(groups[k], wiredBulkMember{desired: device, state: state})
	}
	for name, state := range states {
		if !names[name] || state != "PROVISION" && state != "REPROVISION" {
			return nil, errConfigurationTransition
		}
	}
	keys := make([]key, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].path != keys[j].path {
			return keys[i].path < keys[j].path
		}
		return keys[i].state < keys[j].state
	})
	batches := []wiredBulkBatch{}
	for _, key := range keys {
		members := groups[key]
		// Stable source selectors determine chunk boundaries; native UUID ordering
		// within each chunk remains the existing batch planner's responsibility.
		sort.Slice(members, func(i, j int) bool {
			a, _ := json.Marshal(members[i].desired.reference)
			b, _ := json.Marshal(members[j].desired.reference)
			return string(a) < string(b)
		})
		for start := 0; start < len(members); start += wiredBulkProvisionLimit {
			end := start + wiredBulkProvisionLimit
			if end > len(members) {
				end = len(members)
			}
			batches = append(batches, wiredBulkBatch{path: key.path, members: append([]wiredBulkMember(nil), members[start:end]...)})
		}
	}
	return batches, nil
}
