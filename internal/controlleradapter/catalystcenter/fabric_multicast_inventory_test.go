// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"
)

func multicastObservationFixture() map[string]any {
	return map[string]any{"id": "multicast", "fabricId": "fabric", "virtualNetworkName": "Corp", "ipPoolName": "pool", "ipv4SsmRanges": []any{"233.0.0.0/8", "232.0.0.0/8"}, "multicastRPs": []any{
		map[string]any{"rpDeviceLocation": "FABRIC", "networkDeviceIds": []any{"device-b", "device-a"}, "ipv4Address": "192.0.2.1", "ipv6Address": "2001:db8::1", "isDefaultV4RP": false, "ipv6AsmRanges": []any{"ff3e::/96"}},
		map[string]any{"rpDeviceLocation": "EXTERNAL", "ipv4Address": "192.0.2.2", "ipv4AsmRanges": []any{"239.0.0.0/8"}},
	}}
}
func TestFabricMulticastObservation(t *testing.T) {
	for _, mode := range []string{"valid", "reordered", "omitted-rps", "null-rps", "empty-rps", "unknown-top", "unknown-rp", "missing-id", "missing-pool", "invalid-ssm", "missing-location", "missing-devices", "duplicate-device", "invalid-address", "wrong-family", "external-two-addresses", "external-device", "duplicate-rp", "duplicate-fabric-rp-address-changed", "wrong-flag", "bad-rp-list"} {
		t.Run(mode, func(t *testing.T) {
			model := multicastObservationFixture()
			rows := model["multicastRPs"].([]any)
			fabric := rows[0].(map[string]any)
			external := rows[1].(map[string]any)
			switch mode {
			case "reordered":
				model["multicastRPs"] = []any{external, fabric}
				fabric["networkDeviceIds"] = []any{"device-a", "device-b"}
				model["ipv4SsmRanges"] = []any{"232.0.0.0/8", "233.0.0.0/8"}
			case "omitted-rps":
				delete(model, "multicastRPs")
			case "null-rps":
				model["multicastRPs"] = nil
			case "empty-rps":
				model["multicastRPs"] = []any{}
			case "unknown-top":
				model["newField"] = "opaque"
			case "unknown-rp":
				fabric["newField"] = "opaque"
			case "missing-id":
				delete(model, "id")
			case "missing-pool":
				delete(model, "ipPoolName")
			case "invalid-ssm":
				model["ipv4SsmRanges"] = []any{"224.0.0.0/4"}
			case "missing-location":
				delete(fabric, "rpDeviceLocation")
			case "missing-devices":
				delete(fabric, "networkDeviceIds")
			case "duplicate-device":
				fabric["networkDeviceIds"] = []any{"device-a", "device-a"}
			case "invalid-address":
				fabric["ipv4Address"] = "garbage"
			case "wrong-family":
				fabric["ipv4Address"] = "2001:db8::1"
			case "external-two-addresses":
				external["ipv6Address"] = "2001:db8::2"
			case "external-device":
				external["networkDeviceIds"] = []any{"device-a"}
			case "duplicate-rp":
				model["multicastRPs"] = append(rows, external)
			case "duplicate-fabric-rp-address-changed":
				copy := map[string]any{}
				for k, v := range fabric {
					copy[k] = v
				}
				copy["ipv4Address"] = "192.0.2.9"
				model["multicastRPs"] = append(rows, copy)
			case "wrong-flag":
				fabric["isDefaultV4RP"] = "false"
			case "bad-rp-list":
				model["multicastRPs"] = map[string]any{}
			}
			raw, _ := json.Marshal(model)
			got, err := parseFabricMulticastVNObservation(raw)
			valid := mode == "valid" || mode == "reordered" || mode == "omitted-rps" || mode == "null-rps" || mode == "empty-rps" || mode == "unknown-top" || mode == "unknown-rp"
			if (err == nil) != valid {
				t.Fatal("unexpected observation", err)
			}
			if !valid {
				if !reflect.DeepEqual(got, fabricMulticastVN{}) {
					t.Fatal("partial invalid observation")
				}
				return
			}
			if got.unknownFields != (mode == "unknown-top" || mode == "unknown-rp") {
				t.Fatal("unknown field evidence lost")
			}
			if mode == "omitted-rps" || mode == "null-rps" {
				if _, present := got.fields["multicastRPs"]; present {
					t.Fatal("invented empty evidence")
				}
				return
			}
			rps := got.fields["multicastRPs"].([]map[string]any)
			if mode == "empty-rps" {
				if len(rps) != 0 {
					t.Fatal("lost explicit empty")
				}
				return
			}
			found := false
			for _, rp := range rps {
				if rp["rpDeviceLocation"] == "FABRIC" {
					found = true
					if rp["ipv4Address"] != "192.0.2.1" || rp["ipv6Address"] != "2001:db8::1" || rp["isDefaultV4RP"] != false {
						t.Fatal("allocated addresses/false lost")
					}
					if rp["newField"] != nil || !reflect.DeepEqual(rp["networkDeviceIds"], []string{"device-a", "device-b"}) {
						t.Fatal("unsafe RP projection")
					}
				}
			}
			if !found {
				t.Fatal("fabric RP missing")
			}
			if mode == "reordered" {
				original, _ := json.Marshal(multicastObservationFixture())
				baseline, err := parseFabricMulticastVNObservation(original)
				if err != nil || !reflect.DeepEqual(baseline, got) {
					t.Fatal("set order changes observation")
				}
			}
		})
	}
}

func TestFabricMulticastInventoryPaging(t *testing.T) {
	for _, mode := range []string{"paged", "duplicate-id", "duplicate-key", "late-error", "malformed-late", "empty"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != fabricMulticastVNsPath || r.Method != "GET" {
					t.Error("unexpected request")
					w.WriteHeader(400)
					return
				}
				reads++
				rows := []any{}
				if mode != "empty" {
					if reads == 1 {
						if r.URL.Query().Get("offset") != "1" || r.URL.Query().Get("limit") != "500" {
							t.Error("first page query")
						}
						for i := 0; i < 500; i++ {
							row := multicastObservationFixture()
							row["id"] = fmt.Sprintf("id-%d", i)
							row["virtualNetworkName"] = fmt.Sprintf("VN-%d", i)
							rows = append(rows, row)
						}
					} else {
						if r.URL.Query().Get("offset") != "501" {
							t.Error("second page query")
						}
						if mode == "late-error" {
							w.WriteHeader(500)
							return
						}
						row := multicastObservationFixture()
						if mode == "duplicate-id" {
							row["id"] = "id-0"
						}
						if mode == "duplicate-key" {
							row["virtualNetworkName"] = "VN-0"
						}
						if mode == "malformed-late" {
							delete(row, "fabricId")
						}
						rows = append(rows, row)
					}
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			got, err := c.fabricMulticastVNs(context.Background())
			if mode == "paged" {
				if err != nil || len(got) != 501 || reads != 2 {
					t.Fatal("incomplete paging", err, len(got), reads)
				}
				return
			}
			if mode == "empty" {
				if err != nil || len(got) != 0 {
					t.Fatal("empty", err)
				}
				return
			}
			if err == nil || got != nil {
				t.Fatal("partial inventory returned", err)
			}
		})
	}
}
