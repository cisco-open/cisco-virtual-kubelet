// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package nacmodel

import "testing"

func TestDiscoveryDefaultsPinnedContract(t *testing.T) {
	fields, err := PinnedDiscoveryDefaults()
	if err != nil || len(fields) != 0 {
		t.Fatal("pinned discovery default projection changed", err)
	}
	if defaultsDigest(discoveryTerraform) != DiscoveryTerraformSHA256 || defaultsDigest(pinnedDefaults) != DefaultsSHA256 {
		t.Fatal("pinned source digest mismatch")
	}
	for _, raw := range []string{"defaults: {catalyst_center: {inventory: {discovery: {retry: 1, retry: 2}}}}", "defaults: {catalyst_center: {inventory: {discovery: &x {retry: 1}}}}", "defaults: {catalyst_center: {inventory: {discovery: {}}}, other: {}}", "defaults: {catalyst_center: {inventory: {discovery: {}}}}\n---\n{}"} {
		if _, err := DiscoveryDefaultOverrides([]byte(raw)); err == nil {
			t.Fatal("ambiguous defaults accepted")
		}
	}
}
