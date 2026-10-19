// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "sort"

type fabricPendingEventsIntent struct{ path string }

// Preserve the entire existing fabric-site composition while extracting the
// pinned reconfigure flag. This is private until event execution has physical
// group admission; existing public parsers must not silently discard the flag.
func parseFabricSitesWithPendingEvents(value any) (fabricGatewayModel, []fabricMulticastSiteIntent, []fabricPendingEventsIntent, error) {
	rows, ok := value.([]any)
	if !ok || len(rows) == 0 || len(rows) > 1000 {
		return fabricGatewayModel{}, nil, nil, errInvalidResponse
	}
	base := make([]any, 0, len(rows))
	wants := []fabricPendingEventsIntent{}
	for _, row := range rows {
		site, ok := row.(map[string]any)
		if !ok {
			return fabricGatewayModel{}, nil, nil, errInvalidResponse
		}
		copy := make(map[string]any, len(site))
		for key, item := range site {
			if key != "reconfigure" {
				copy[key] = item
			}
		}
		if flag, present := site["reconfigure"]; present {
			enabled, ok := flag.(bool)
			if !ok {
				return fabricGatewayModel{}, nil, nil, errInvalidResponse
			}
			if enabled {
				path, ok := site["name"].(string)
				if !ok {
					return fabricGatewayModel{}, nil, nil, errInvalidResponse
				}
				wants = append(wants, fabricPendingEventsIntent{path: path})
			}
		}
		base = append(base, copy)
	}
	model, multicast, err := parseFabricSitesWithMulticast(base)
	if err != nil {
		return fabricGatewayModel{}, nil, nil, err
	}
	// Pending-event recovery does not yet verify wireless multicast convergence.
	// Report support must not broaden that execution path by accepting an effect
	// whose desired state its recovery verifier cannot consume.
	for _, site := range multicast {
		if site.intent.wireless != nil {
			return fabricGatewayModel{}, nil, nil, errConfigurationDeviceAdmission
		}
	}
	sort.Slice(wants, func(i, j int) bool { return wants[i].path < wants[j].path })
	return model, multicast, wants, nil
}
