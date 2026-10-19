// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"strings"
)

func (c *client) checkSiteLANAutomation(ctx context.Context, target Site, sites map[string]Site) error {
	sessions, err := c.lanSessions(ctx)
	if err != nil {
		return err
	}
	return checkSiteLANSessionReferences(target, sites, sessions)
}

// STOP and free-form status text never release a retained session reference.
// Complete explicit scope can establish an unrelated branch; missing legacy
// discoveryDevices or unmapped fields cannot establish absence of overrides.
func checkSiteLANSessionReferences(target Site, sites map[string]Site, sessions []lanSession) error {
	byPath := map[string]Site{}
	for id, site := range sites {
		if id != site.ID || !validAPIID(id) || !validSitePath(site.NameHierarchy) {
			return errSiteRetirementReferenced
		}
		if _, duplicate := byPath[site.NameHierarchy]; duplicate {
			return errSiteRetirementReferenced
		}
		byPath[site.NameHierarchy] = site
	}
	if actual, exists := sites[target.ID]; !exists || actual.NameHierarchy != target.NameHierarchy {
		return errSiteRetirementReferenced
	}
	related := func(path string) bool {
		return path == target.NameHierarchy || strings.HasPrefix(path, target.NameHierarchy+"/") || strings.HasPrefix(target.NameHierarchy, path+"/")
	}
	for _, session := range sessions {
		if session.unmapped || session.DiscoveryDevices == nil || session.Action != "START" && session.Action != "STOP" {
			return errSiteRetirementReferenced
		}
		if _, known := byPath[session.Site]; !known || related(session.Site) {
			return errSiteRetirementReferenced
		}
		for _, device := range *session.DiscoveryDevices {
			if device.unmapped {
				return errSiteRetirementReferenced
			}
			path := session.Site
			if device.SiteID != "" {
				site, known := sites[device.SiteID]
				if !known {
					return errSiteRetirementReferenced
				}
				path = site.NameHierarchy
			}
			if device.Site != "" {
				site, known := byPath[device.Site]
				if !known || device.SiteID != "" && site.ID != device.SiteID {
					return errSiteRetirementReferenced
				}
				path = device.Site
			}
			if related(path) {
				return errSiteRetirementReferenced
			}
		}
	}
	return nil
}
