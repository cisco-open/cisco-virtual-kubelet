// Copyright © 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Identity evidence only. Native masked values cannot prove password versions
// or rendered device authentication. Definitions require their separate owned
// credential/version receipts and are not accepted by this private bridge.
func wiredBulkCredentialContextSupported(intent *networkReportIntent) bool {
	if intent == nil {
		return true
	}
	if len(intent.credentials) != 0 {
		return false
	}
	for _, assignment := range intent.assignments {
		if assignment.name == "" {
			return false // removal/inheritance mutation remains unqualified
		}
	}
	return true
}

type wiredBulkCredentialBinding struct {
	SiteID, ParentID, Path, Kind, Slot, CredentialID string
	Inherited, Disabled                              bool
}
type wiredBulkCredentialSnapshot struct {
	Bindings []wiredBulkCredentialBinding
	Metadata []globalCredentialMetadata
}

func (wiredBulkCredentialSnapshot) String() string   { return "<private bulk credential identities>" }
func (wiredBulkCredentialSnapshot) GoString() string { return "<private bulk credential identities>" }

func (s *wiredBulkCredentialSnapshot) digest() (string, error) {
	if s == nil || len(s.Bindings) == 0 || len(s.Metadata) == 0 {
		return "", errConfigurationTransition
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}

func (c *client) observeWiredBulkCredentials(ctx context.Context, model wiredBulkSourceModel) (*wiredBulkCredentialSnapshot, error) {
	if model.network == nil || len(model.network.assignments) == 0 || !wiredBulkCredentialContextSupported(model.network) {
		return nil, errWiredBulkNetworkContextUnavailable
	}
	// Reuse the assignment executor's exact local-slot and named-ID checks.
	observations, err := (&siteCredentialExecutor{client: c, desired: model.network.assignments}).observe(ctx)
	if err != nil {
		return nil, err
	}
	for _, observation := range observations {
		if !observation.matches || observation.pending {
			return nil, errConfigurationStale
		}
	}
	catalog, err := c.ListSites(ctx)
	if err != nil {
		return nil, err
	}
	sites := map[string]Site{}
	for _, site := range catalog {
		if _, exists := sites[site.NameHierarchy]; exists {
			return nil, errInvalidResponse
		}
		sites[site.NameHierarchy] = site
	}
	metadata, err := c.globalCredentials(ctx)
	if err != nil {
		return nil, err
	}
	result := &wiredBulkCredentialSnapshot{}
	seenBindings, seenMetadata := map[string]bool{}, map[string]bool{}
	slots := map[string]map[string]siteCredentialState{}
	// Include each declared assignment itself and every selected device site's
	// effective chain. An undeclared child override must not masquerade as the
	// ancestor assignment requested in the source.
	paths := map[string]bool{}
	for _, assignment := range model.network.assignments {
		paths[assignment.site] = true
	}
	for _, device := range model.inventory.devices {
		paths[device.site] = true
	}
	for path := range paths {
		selected := map[string]siteCredentialIntent{}
		for _, assignment := range model.network.assignments {
			if siteWithin(path, assignment.site) {
				previous, exists := selected[assignment.kind]
				if !exists || strings.Count(assignment.site, "/") > strings.Count(previous.site, "/") {
					selected[assignment.kind] = assignment
				}
			}
		}
		for kind, want := range selected {
			credential, exists := metadata[kind][want.name]
			if !exists || !validAPIID(credential.ID) {
				return nil, errConfigurationStale
			}
			if !seenMetadata[credential.ID] {
				result.Metadata = append(result.Metadata, credential)
				seenMetadata[credential.ID] = true
			}
			current := path
			resolved := false
			for {
				site, exists := sites[current]
				if !exists || !validAPIID(site.ID) {
					return nil, errConfigurationStale
				}
				if slots[site.ID] == nil {
					slots[site.ID], err = c.siteCredentials(ctx, site.ID)
					if err != nil {
						return nil, err
					}
				}
				state := slots[site.ID][kind]
				key := site.ID + "/" + kind
				if !seenBindings[key] {
					result.Bindings = append(result.Bindings, wiredBulkCredentialBinding{site.ID, site.ParentID, current, site.Type, kind, state.id, state.inherited, state.unset})
					seenBindings[key] = true
				}
				if !resolved && !state.inherited {
					if state.unset || state.id != credential.ID {
						return nil, errConfigurationStale
					}
					resolved = true
				}
				if site.Type == "global" {
					if !resolved {
						return nil, errConfigurationStale
					}
					break
				}
				parent := parentSitePath(current)
				if parentSite, exists := sites[parent]; !exists || parentSite.ID != site.ParentID {
					return nil, errConfigurationStale
				}
				current = parent
			}
		}
	}
	sort.Slice(result.Bindings, func(i, j int) bool {
		a, b := result.Bindings[i], result.Bindings[j]
		return a.SiteID+"/"+a.Slot < b.SiteID+"/"+b.Slot
	})
	sort.Slice(result.Metadata, func(i, j int) bool { return result.Metadata[i].ID < result.Metadata[j].ID })
	return result, nil
}
