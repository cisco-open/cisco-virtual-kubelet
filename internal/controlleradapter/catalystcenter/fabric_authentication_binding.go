// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/hex"
	"strings"
)

const fabricAuthManagedPrefix = fabricAuthResourceFamily + "v2:"

func fabricAuthDigest(value any) string {
	return authConfigurationValueDigest(value)
}

// Hash only nonsensitive modeled fields and native boolean preservation. ACL
// descriptions are neither published nor hashed. Contract order is semantic.
func fabricAuthProfileDigest(p authProfile) string {
	return authProfileConfigurationDigest(p)
}

func fabricAuthManagedResource(w fabricAuthIntent, state fabricAuthState, physicalDigest string) string {
	result := authExpectedResultProfile(w.profile, state.profile)
	logical := fabricAuthDigest([]string{w.site.path, w.profile.Name})
	scope := fabricAuthDigest([]string{state.siteID, state.fabricID, state.profile.ID})
	return fabricAuthManagedPrefix + strings.Join([]string{logical, scope, fabricAuthProfileDigest(state.profile), fabricAuthProfileDigest(result), physicalDigest}, ":")
}

func parseFabricAuthManagedResource(resource string) ([]string, error) {
	suffix, ok := strings.CutPrefix(resource, fabricAuthManagedPrefix)
	parts := strings.Split(suffix, ":")
	if !ok || len(parts) != 5 {
		return nil, errConfigurationTransition
	}
	for _, part := range parts {
		if len(part) != 64 || strings.ToLower(part) != part {
			return nil, errConfigurationTransition
		}
		if _, err := hex.DecodeString(part); err != nil {
			return nil, errConfigurationTransition
		}
	}
	return parts, nil
}
