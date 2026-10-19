// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import "errors"

var errPlannedAPUnmappedUpdate = errors.New("planned AP native state contains unmapped update fields")

// Unknown native fields are retained in the private dispatch/verification view,
// but cannot be silently dropped by the bounded bulkChange payload. The qualified
// antenna sensor flag is observed and omitted, never invented or overwritten.
func validatePlannedAPUpdateFootprint(native map[string]any) error {
	if native == nil {
		return errInvalidResponse
	}
	allowed := func(body map[string]any, keys ...string) bool {
		for key := range body {
			found := false
			for _, known := range keys {
				if key == known {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
		return true
	}
	if !allowed(native, "id", "name", "type", "macAddress", "position", "radios") {
		return errPlannedAPUnmappedUpdate
	}
	position, ok := native["position"].(map[string]any)
	if !ok || !allowed(position, "x", "y", "z") {
		return errPlannedAPUnmappedUpdate
	}
	radios, ok := native["radios"].([]any)
	if !ok {
		return errInvalidResponse
	}
	for _, value := range radios {
		radio, ok := value.(map[string]any)
		if !ok || !allowed(radio, "id", "bands", "channel", "txPower", "antenna") {
			return errPlannedAPUnmappedUpdate
		}
		antenna, ok := radio["antenna"].(map[string]any)
		if !ok || !allowed(antenna, "name", "azimuth", "elevation", "elevationAccelerometer") {
			return errPlannedAPUnmappedUpdate
		}
		if flag, present := antenna["elevationAccelerometer"]; present {
			if _, ok := flag.(bool); !ok {
				return errInvalidResponse
			}
		}
	}
	return nil
}
