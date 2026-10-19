// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestFabricSDAHandoffModel(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"omitted", `{"sda_transit":"Transit"}`, true},
		{"explicit", `{"sda_transit":"Transit","affinity_id_prime":0,"affinity_id_decider":2147483647,"connected_to_internet":false,"multicast_over_transit":true}`, true},
		{"negative", `{"sda_transit":"Transit","affinity_id_prime":-1}`, false},
		{"overflow", `{"sda_transit":"Transit","affinity_id_decider":2147483648}`, false},
		{"fraction", `{"sda_transit":"Transit","affinity_id_prime":1.5}`, false},
		{"string-number", `{"sda_transit":"Transit","affinity_id_prime":"1"}`, false},
		{"string-boolean", `{"sda_transit":"Transit","connected_to_internet":"false"}`, false},
		{"null", `{"sda_transit":"Transit","multicast_over_transit":null}`, true},
		{"unknown", `{"sda_transit":"Transit","unexpected":true}`, false},
		{"missing", `{"connected_to_internet":false}`, false},
		{"empty", `{"sda_transit":""}`, false},
		{"shape", `[]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(tc.raw), &value); err != nil {
				t.Fatal(err)
			}
			got, err := parseFabricSDAHandoff("border", value)
			if (err == nil) != tc.valid {
				t.Fatal("parse outcome", err)
			}
			if !tc.valid {
				if got != nil {
					t.Fatal("partial intent")
				}
				return
			}
			if got.Device != "border" || got.Transit != "Transit" {
				t.Fatal("lost identity")
			}
			if (tc.name == "omitted" || tc.name == "null") && (got.Prime != nil || got.Decider != nil || got.Internet != nil || got.Multicast != nil) {
				t.Fatal("invented defaults")
			}
			if tc.name == "explicit" {
				if got.Prime == nil || *got.Prime != 0 || got.Decider == nil || *got.Decider != 2147483647 || got.Internet == nil || *got.Internet || got.Multicast == nil || !*got.Multicast {
					t.Fatal("lost explicit values")
				}
				value.(map[string]any)["affinity_id_prime"] = float64(100)
				if *got.Prime != 0 {
					t.Fatal("aliased caller input")
				}
			}
			if result, err := parseFabricSDAHandoff("", value); err == nil || result != nil {
				t.Fatal("invalid device accepted")
			}
		})
	}
}
