// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"bytes"
	"encoding/json"
)

// Pinned NetAsCode 0.5.0 border-level SDA transit settings. Pointers preserve
// omission separately from explicit zero/false; this resource has no pinned
// defaults for those fields. This is intent only, not native execution authority.
type fabricSDAHandoffIntent struct {
	Device    string `json:"-"`
	Transit   string `json:"sda_transit"`
	Prime     *int64 `json:"affinity_id_prime,omitempty"`
	Decider   *int64 `json:"affinity_id_decider,omitempty"`
	Internet  *bool  `json:"connected_to_internet,omitempty"`
	Multicast *bool  `json:"multicast_over_transit,omitempty"`
}

func parseFabricSDAHandoff(device string, value any) (*fabricSDAHandoffIntent, error) {
	if !validProfileName(device) {
		return nil, errInvalidResponse
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, errInvalidResponse
	}
	var result fabricSDAHandoffIntent
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || !validProfileName(result.Transit) {
		return nil, errInvalidResponse
	}
	for _, affinity := range []*int64{result.Prime, result.Decider} {
		if !validFabricSDAAffinity(affinity) {
			return nil, errInvalidResponse
		}
	}
	result.Device = device
	return &result, nil
}
