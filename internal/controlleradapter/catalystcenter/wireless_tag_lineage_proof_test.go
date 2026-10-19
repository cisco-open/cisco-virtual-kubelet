// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWirelessTagLineageActionClasses(t *testing.T) {
	for _, mode := range []string{"recreate", "write", "members", "delete"} {
		for _, action := range []string{"Create", "Update", "Delete"} {
			t.Run(mode+"/"+action, func(t *testing.T) {
				baseMode := mode
				if mode == "recreate" {
					baseMode = "write"
				}
				op := boundTagEnvelopeFixture(baseMode, action)
				resource, err := wirelessTagLineageResource(op.Resource, mode, strings.Repeat("e", 64))
				if err != nil {
					t.Fatal(err)
				}
				op.Resource = resource
				valid := mode == "recreate" && action == "Create" || (mode == "write" || mode == "members") && action == "Update" || mode == "delete" && action == "Delete"
				if wirelessTagPrivateEnvelope(op) != valid {
					t.Fatal("lineage envelope changed action class")
				}
				if mode == "members" || mode == "delete" {
					ids, err := decodeWirelessTagMembershipScope(resource)
					if err != nil || len(ids) != 1 || ids[0] != "site" {
						t.Fatal("lineage digest displaced affected membership scope", err)
					}
				}
			})
		}
	}
}

func TestWirelessTagLineageMACDomain(t *testing.T) {
	key := strings.Repeat("a", 64)
	comparison := &templateInputComparison{key: []byte(strings.Repeat("k", 32)), keyID: strings.Repeat("c", 64)}
	for _, mode := range []string{"recreate", "write", "members", "delete"} {
		t.Run(mode, func(t *testing.T) {
			baseMode := mode
			if mode == "recreate" {
				baseMode = "write"
			}
			op := boundTagEnvelopeFixture(baseMode, "Update")
			baseline := map[string]any{"id": "tag", "state": "same-native-fields"}
			legacy, err := wirelessTagMAC(comparison, "wireless-policy-tags/", "profile", key, "tag", baseline)
			if err != nil {
				t.Fatal(err)
			}
			seen := map[string]bool{legacy: true}
			for _, digit := range []string{"e", "f"} {
				resource, err := wirelessTagLineageResource(op.Resource, mode, strings.Repeat(digit, 64))
				if err != nil {
					t.Fatal(err)
				}
				bound, err := wirelessTagOperationBaseline(resource, baseline)
				if err != nil {
					t.Fatal(err)
				}
				data, _ := json.Marshal(bound)
				want := `{"incarnation":"` + strings.Repeat(digit, 64) + `","native":{"id":"tag","state":"same-native-fields"},"version":5}`
				if string(data) != want {
					t.Fatal("lineage MAC encoding changed")
				}
				mac, err := wirelessTagMAC(comparison, "wireless-policy-tags/", "profile", key, "tag", bound)
				if err != nil || seen[mac] {
					t.Fatal("lineage MAC can be reused for a different incarnation or legacy baseline", err)
				}
				seen[mac] = true
				if _, err := wirelessTagOperationBaseline(resource, nil); err == nil {
					t.Fatal("missing native baseline accepted")
				}
				bad := strings.Replace(resource, strings.Repeat(digit, 64), "malformed", 1)
				if _, err := wirelessTagOperationBaseline(bad, baseline); err == nil {
					t.Fatal("malformed lineage silently used legacy MAC semantics")
				}
			}
		})
	}
}
