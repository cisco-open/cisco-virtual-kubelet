// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "sort"

type fabricMulticastSiteEvidence struct {
	members     []fabricDeviceMembership
	poolDigests map[string]string
}

// Evaluate the prospective complete site, retaining VNs outside the manifest.
// Proposed entries are normalized full payloads; the caller must first apply
// ownership-aware payload construction. The inventory must be fully paginated.
func validateFabricMulticastSite(fabricID, siteID string, observed map[fabricMulticastVNKey]fabricMulticastVN, proposed []fabricMulticastVN, pools *anycastPoolInventory, memberships map[string]fabricDeviceMembership) (fabricMulticastSiteEvidence, error) {
	zero := fabricMulticastSiteEvidence{}
	if !validAPIID(fabricID) || !validAPIID(siteID) || pools == nil {
		return zero, errInvalidResponse
	}
	combined := map[string]fabricMulticastVN{}
	for key, row := range observed {
		if key.fabricID != row.FabricID || key.network != row.Network {
			return zero, errInvalidResponse
		}
		if row.FabricID != fabricID {
			continue
		}
		if row.unknownFields || !validAPIID(row.ID) {
			return zero, errInvalidResponse
		}
		combined[row.Network] = row
	}
	seen := map[string]bool{}
	for _, row := range proposed {
		if row.FabricID != fabricID || !validAPIID(row.ID) || !validProfileName(row.Network) || seen[row.Network] || row.unknownFields {
			return zero, errInvalidResponse
		}
		seen[row.Network] = true
		if before, exists := combined[row.Network]; exists && (before.ID != row.ID || before.Pool != row.Pool) {
			return zero, errConfigurationStale
		}
		combined[row.Network] = row
	}
	out := fabricMulticastSiteEvidence{poolDigests: map[string]string{}}
	ids := map[string]bool{}
	dual := false
	for name, row := range combined {
		if err := validateFabricMulticastMerged(row.fields); err != nil {
			return zero, err
		}
		binding, err := pools.bindMulticastPool(fabricMulticastVNIntent{name: name, pool: row.Pool}, siteID, &row)
		if err != nil {
			return zero, err
		}
		out.poolDigests[name] = binding.digest
		rps, known := row.fields["multicastRPs"].([]map[string]any)
		if !known {
			return zero, errInvalidResponse
		}
		for _, rp := range rps {
			if rp["rpDeviceLocation"] != "FABRIC" {
				continue
			}
			devices, ok := rp["networkDeviceIds"].([]string)
			if !ok || len(devices) == 0 {
				return zero, errInvalidResponse
			}
			// Only a VN actually using fabric RPs constrains the shared RP set.
			dual = dual || binding.primary.V6 != nil
			for _, id := range devices {
				ids[id] = true
			}
		}
	}
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	if err := validateFabricMulticastRPSet(fabricID, ordered, memberships, dual); err != nil {
		return zero, err
	}
	for _, id := range ordered {
		out.members = append(out.members, memberships[id])
	}
	return out, nil
}
