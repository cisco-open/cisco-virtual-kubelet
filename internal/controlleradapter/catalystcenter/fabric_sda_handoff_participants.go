// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

type fabricSDAParticipant struct {
	Site     fabricSite
	Handoffs []fabricSDAHandoff
	Members  map[string]fabricDeviceConfiguration
}

// Scan every native fabric to discover other sites sharing the selected transit.
// Include all members of each participating fabric, not only its border. The
// caller must resolve their physical identities and repeat discovery at dispatch.
func (c *client) fabricSDAParticipants(ctx context.Context, localID, transitID string) ([]string, string, error) {
	ids, digest, _, err := c.fabricSDAParticipantEvidence(ctx, localID, transitID, nil)
	return ids, digest, err
}

func (c *client) fabricSDAParticipantEvidence(ctx context.Context, localID, transitID string, created *fabricSDAHandoff) ([]string, string, string, error) {
	if !validAPIID(localID) || !validAPIID(transitID) {
		return nil, "", "", errInvalidResponse
	}
	if created != nil && (created.FabricID != localID || created.TransitID != transitID || !validAPIID(created.DeviceID)) {
		return nil, "", "", errInvalidResponse
	}
	sites, err := c.fabricSites(ctx)
	if err != nil {
		return nil, "", "", err
	}
	if len(sites) > 1000 {
		return nil, "", "", errInvalidResponse
	}
	ordered := make([]fabricSite, 0, len(sites))
	found := false
	for _, site := range sites {
		ordered = append(ordered, site)
		found = found || site.ID == localID
	}
	if !found {
		return nil, "", "", errConfigurationStale
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].ID < ordered[j].ID })
	evidence := []fabricSDAParticipant{}
	ids := []string{}
	for _, site := range ordered {
		rows, err := c.fabricSDAHandoffs(ctx, site.ID, "")
		if err != nil {
			return nil, "", "", err
		}
		linked := []fabricSDAHandoff{}
		for _, row := range rows {
			if row.TransitID == transitID {
				linked = append(linked, row)
			}
		}
		if site.ID != localID && len(linked) == 0 {
			continue
		}
		sort.Slice(linked, func(i, j int) bool { return linked[i].DeviceID < linked[j].DeviceID })
		members, err := c.fabricDeviceConfigurations(ctx, site.ID)
		if err != nil {
			return nil, "", "", err
		}
		for _, row := range linked {
			member, ok := members[row.DeviceID]
			if !ok || !slices.Contains(member.Roles, "BORDER_NODE") || member.Border == nil || !slices.Contains(member.Border.Types, "LAYER_3") {
				return nil, "", "", errConfigurationStale
			}
		}
		for id := range members {
			ids = append(ids, id)
		}
		evidence = append(evidence, fabricSDAParticipant{Site: site, Handoffs: linked, Members: members})
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) > 1000 {
		return nil, "", "", errInvalidResponse
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return nil, "", "", errInvalidResponse
	}
	baseline := make([]fabricSDAParticipant, len(evidence))
	for i, site := range evidence {
		baseline[i] = site
		baseline[i].Handoffs = []fabricSDAHandoff{}
		for _, row := range site.Handoffs {
			if created == nil || !fabricSDAHandoffMatches(*created, row) {
				baseline[i].Handoffs = append(baseline[i].Handoffs, row)
			}
		}
	}
	recovery, err := json.Marshal(baseline)
	if err != nil {
		return nil, "", "", errInvalidResponse
	}
	return ids, fmt.Sprintf("%x", sha256.Sum256(raw)), fmt.Sprintf("%x", sha256.Sum256(recovery)), nil
}
