// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"errors"
	"strings"
)

var errWirelessTagScopeConflict = errors.New("wireless tag scope overlaps another tag")

// Site tags are unique per site. Shared-site policy tags are qualified only with
// disjoint zone selections; ancestor/descendant assignments also affect
// the same zone. Never use ordering to silently replace another definition.
func wirelessTagScopesConflict(left, right map[string]any, sites map[string]string, policy bool) (bool, error) {
	paths := map[string]string{}
	for path, id := range sites {
		if !validSitePath(path) || !validAPIID(id) {
			return false, errInvalidResponse
		}
		if old, exists := paths[id]; exists && old != path {
			return false, errInvalidResponse
		}
		paths[id] = path
	}
	a, ok := left["siteIds"].([]string)
	if !ok {
		return false, errInvalidResponse
	}
	b, ok := right["siteIds"].([]string)
	if !ok {
		return false, errInvalidResponse
	}
	for _, ids := range [][]string{a, b} {
		for _, id := range ids {
			if paths[id] == "" {
				return false, errInvalidResponse
			}
		}
	}
	related := false
	for _, x := range a {
		for _, y := range b {
			if x == y || policy && (strings.HasPrefix(paths[x], paths[y]+"/") || strings.HasPrefix(paths[y], paths[x]+"/")) {
				related = true
			}
		}
	}
	if !related || !policy {
		return related, nil
	}
	zones := func(fields map[string]any) ([]string, error) {
		values, ok := fields["apZones"].([]string)
		if !ok {
			return nil, errInvalidResponse
		}
		if len(values) == 0 {
			return []string{"default-zone"}, nil
		}
		for _, name := range values {
			if !validProfileName(name) {
				return nil, errInvalidResponse
			}
		}
		return values, nil
	}
	az, err := zones(left)
	if err != nil {
		return false, err
	}
	bz, err := zones(right)
	if err != nil {
		return false, err
	}
	for _, x := range az {
		for _, y := range bz {
			if x == y {
				return true, nil
			}
		}
	}
	return false, nil
}

func refuseWirelessTagScopeOverlap(payload map[string]any, others map[string]map[string]any, sites map[string]string, policy bool) error {
	for _, other := range others {
		conflict, err := wirelessTagScopesConflict(payload, other, sites, policy)
		if err != nil {
			return err
		}
		if conflict {
			return errWirelessTagScopeConflict
		}
	}
	return nil
}
