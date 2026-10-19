// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestFabricMulticastPayload(t *testing.T) {
	for _, mode := range []string{"create", "update", "noop", "missing-device", "aliased-device", "wrong-fabric", "wrong-pool", "unknown-observation", "missing-rp-evidence", "remove-rp", "remove-ssm", "remove-asm", "preserve-omitted", "missing-allocated-address", "retained-overlap"} {
		t.Run(mode, func(t *testing.T) {
			model := multicastVNFixture()
			model["ip_pool_name"] = "pool"
			// Align explicit intent with the observation fixture while retaining unowned fields.
			model["ipv4_ssm_ranges"] = []any{"232.0.0.0/8", "233.0.0.0/8"}
			rps := model["multicast_rps"].([]any)
			external := rps[0].(map[string]any)
			fabric := rps[1].(map[string]any)
			external["ipv4_address"] = "192.0.2.2"
			delete(external, "is_default_v4_rp")
			delete(fabric, "is_default_v6_rp")
			native := multicastObservationFixture()
			ids := map[string]string{"border-a": "device-a", "border-b": "device-b"}
			switch mode {
			case "update":
				external["ipv4_asm_ranges"] = []any{"238.0.0.0/8", "239.0.0.0/8"}
			case "missing-device":
				delete(ids, "border-b")
			case "aliased-device":
				ids["border-b"] = "device-a"
			case "wrong-fabric":
				native["fabricId"] = "replacement"
			case "wrong-pool":
				native["ipPoolName"] = "replacement"
			case "unknown-observation":
				native["newField"] = "opaque"
			case "missing-rp-evidence":
				delete(native, "multicastRPs")
			case "remove-rp":
				model["multicast_rps"] = []any{external}
			case "remove-ssm":
				model["ipv4_ssm_ranges"] = []any{"232.0.0.0/8"}
			case "remove-asm":
				external["ipv4_asm_ranges"] = []any{}
			case "preserve-omitted":
				delete(model, "multicast_rps")
				delete(model, "ipv4_ssm_ranges")
			case "missing-allocated-address":
				delete(native["multicastRPs"].([]any)[0].(map[string]any), "ipv4Address")
			case "retained-overlap":
				delete(model, "multicast_rps")
				model["ipv4_ssm_ranges"] = []any{"232.0.0.0/8", "233.0.0.0/8", "239.0.0.0/8"}
			}
			want, err := parseFabricMulticastVN(model)
			if err != nil {
				t.Fatal("fixture intent", err)
			}
			raw, _ := json.Marshal(native)
			observed, err := parseFabricMulticastVNObservation(raw)
			if err != nil {
				t.Fatal("fixture observation", err)
			}
			baseline, _ := json.Marshal(observed.fields)
			prior := &observed
			if mode == "create" {
				prior = nil
			}
			body, err := fabricMulticastPayload(want, "fabric", ids, prior)
			valid := mode == "create" || mode == "update" || mode == "noop" || mode == "preserve-omitted"
			if (err == nil) != valid {
				t.Fatal("unexpected payload", err)
			}
			after, _ := json.Marshal(observed.fields)
			if string(after) != string(baseline) {
				t.Fatal("mutated readback")
			}
			if !valid {
				matches, err := fabricMulticastMatches(want, "fabric", ids, observed)
				if err == nil || matches {
					t.Fatal("invalid evidence verified", err)
				}
				if body != nil {
					t.Fatal("partial invalid payload")
				}
				return
			}
			if mode == "create" {
				if body["id"] != nil {
					t.Fatal("create emitted native ID")
				}
				for _, rp := range body["multicastRPs"].([]map[string]any) {
					if rp["rpDeviceLocation"] == "FABRIC" && (rp["ipv4Address"] != nil || rp["ipv6Address"] != nil) {
						t.Fatal("create emitted allocated address")
					}
				}
				return
			}
			if body["id"] != "multicast" {
				t.Fatal("lost update identity")
			}
			for _, rp := range body["multicastRPs"].([]map[string]any) {
				if rp["rpDeviceLocation"] == "FABRIC" && (rp["ipv6Address"] != "2001:db8::1" || rp["isDefaultV4RP"] != false) {
					t.Fatal("lost allocated address or omitted setting")
				}
			}
			matches, err := fabricMulticastMatches(want, "fabric", ids, observed)
			if err != nil || matches != (mode != "update") {
				t.Fatal("incorrect readback comparison", matches, err)
			}
			if mode == "preserve-omitted" && !reflect.DeepEqual(body, observed.fields) {
				t.Fatal("omitted fields changed")
			}
		})
	}
}
