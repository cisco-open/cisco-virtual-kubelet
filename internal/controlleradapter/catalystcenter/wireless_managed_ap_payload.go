// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "sort"

type wirelessManagedAPIntent struct{ primary, secondary, anchor []string }
type wirelessManagedAPSnapshot struct{ primary, secondary, anchor map[string]string }

// Build complete replacement sets for the selected API. Omitted intent retains
// the observed set. Explicit membership removal needs ownership evidence and
// remains blocked; it must not silently become an additive update.
func wirelessManagedAPSet(want []string, observed, sites map[string]string) ([]string, error) {
	if observed == nil || sites == nil {
		return nil, errInvalidResponse
	}
	ids := map[string]bool{}
	for path, id := range observed {
		if !validSitePath(path) || path == "Global" || !validAPIID(id) || sites[path] != id || ids[id] {
			return nil, errConfigurationStale
		}
		ids[id] = true
	}
	if want != nil {
		if len(want) > 10000 {
			return nil, errInvalidResponse
		}
		next := map[string]bool{}
		for _, path := range want {
			id := sites[path]
			if !validSitePath(path) || path == "Global" || !validAPIID(id) || next[id] {
				return nil, errInvalidResponse
			}
			next[id] = true
		}
		for id := range ids {
			if !next[id] {
				return nil, errConfigurationOwned
			}
		}
		ids = next
	}
	out := make([]string, 0, len(ids))
	for id := range ids {
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}
func (want wirelessManagedAPIntent) payload(observed wirelessManagedAPSnapshot, sites map[string]string, anchor bool) (map[string][]string, error) {
	if anchor {
		if want.anchor == nil {
			return nil, errConfigurationTransition
		}
		ids, err := wirelessManagedAPSet(want.anchor, observed.anchor, sites)
		if err != nil {
			return nil, err
		}
		return map[string][]string{"anchorManagedAPLocationsSiteIds": ids}, nil
	}
	if want.primary == nil && want.secondary == nil {
		return nil, errConfigurationTransition
	}
	primary, err := wirelessManagedAPSet(want.primary, observed.primary, sites)
	if err != nil {
		return nil, err
	}
	secondary, err := wirelessManagedAPSet(want.secondary, observed.secondary, sites)
	if err != nil {
		return nil, err
	}
	return map[string][]string{"primaryManagedAPLocationsSiteIds": primary, "secondaryManagedAPLocationsSiteIds": secondary}, nil
}
