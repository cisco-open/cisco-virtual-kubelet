// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

func anycastObservationFixture() map[string]any {
	return map[string]any{"id": "gateway", "fabricId": "fabric", "virtualNetworkName": "Corp", "ipPoolName": "Pool", "vlanName": "10.0.0.0/24-Corp", "vlanId": 150, "trafficType": "DATA", "isCriticalPool": false}
}
func TestAnycastObservationEvidence(t *testing.T) {
	for _, mode := range []string{"minimal", "secondary", "empty-secondary", "null-secondary", "unknown", "unknown-secondary", "missing-id", "missing-vlan", "bad-boolean", "bad-vlan", "duplicate-pool", "duplicate-order", "wrong-order", "infra", "infra-secondary", "bad-pool-type"} {
		t.Run(mode, func(t *testing.T) {
			m := anycastObservationFixture()
			switch mode {
			case "secondary":
				m["additionalIpPools"] = []any{map[string]any{"name": "Third", "order": 3}, map[string]any{"name": "Second", "order": 2}}
			case "empty-secondary":
				m["additionalIpPools"] = []any{}
			case "null-secondary":
				m["additionalIpPools"] = nil
			case "unknown":
				m["futureFlag"] = true
			case "unknown-secondary":
				m["additionalIpPools"] = []any{map[string]any{"name": "Second", "order": 2, "futureFlag": true}}
			case "missing-id":
				delete(m, "id")
			case "missing-vlan":
				delete(m, "vlanId")
			case "bad-boolean":
				m["isCriticalPool"] = "false"
			case "bad-vlan":
				m["vlanId"] = 2046
			case "duplicate-pool":
				m["additionalIpPools"] = []any{map[string]any{"name": "Pool", "order": 2}}
			case "duplicate-order":
				m["additionalIpPools"] = []any{map[string]any{"name": "Second", "order": 2}, map[string]any{"name": "Third", "order": 2}}
			case "wrong-order":
				m["additionalIpPools"] = []any{map[string]any{"name": "Second", "order": 1}}
			case "infra":
				m["virtualNetworkName"] = "INFRA_VN"
				m["poolType"] = "FABRIC_AP"
				delete(m, "isCriticalPool")
			case "infra-secondary":
				m["virtualNetworkName"] = "INFRA_VN"
				m["poolType"] = "FABRIC_AP"
				m["additionalIpPools"] = []any{map[string]any{"name": "Second", "order": 2}}
			case "bad-pool-type":
				m["poolType"] = "FABRIC_AP"
			}
			raw, _ := json.Marshal(m)
			got, err := parseAnycastGatewayObservation(raw)
			valid := mode == "minimal" || mode == "secondary" || mode == "empty-secondary" || mode == "null-secondary" || mode == "unknown" || mode == "unknown-secondary" || mode == "infra"
			if (err == nil) != valid {
				t.Fatal("unexpected observation", err)
			}
			if !valid {
				if got.fields != nil {
					t.Fatal("partial malformed observation")
				}
				return
			}
			if (mode == "unknown" || mode == "unknown-secondary") != got.unknownFields {
				t.Fatal("unmodeled evidence lost")
			}
			if mode == "minimal" || mode == "null-secondary" {
				if got.secondaryKnown || got.fields["isWirelessPool"] != nil || got.fields["autoGenerateVlanName"] != nil {
					t.Fatal("invented observation")
				}
			}
			if mode == "empty-secondary" && (!got.secondaryKnown || len(got.fields["additionalIpPools"].([]map[string]any)) != 0) {
				t.Fatal("empty inventory evidence lost")
			}
			if mode == "secondary" {
				pools := got.fields["additionalIpPools"].([]map[string]any)
				if !got.secondaryKnown || pools[0]["name"] != "Second" || pools[1]["order"] != 3 {
					t.Fatal("secondary order normalization failed")
				}
			}
		})
	}
}
func TestAnycastInventoryCompleteness(t *testing.T) {
	for _, mode := range []string{"paged", "repeated", "partial", "duplicate-pool", "duplicate-vlan", "duplicate-name", "other-fabric"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != anycastGatewaysPath || r.Method != "GET" {
					t.Error("unexpected API")
					w.WriteHeader(404)
					return
				}
				calls++
				offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
				if r.URL.Query().Get("limit") != "50" || offset < 1 {
					t.Error("unbounded pagination")
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				if mode == "repeated" {
					offset = 1
				}
				rows := []any{}
				for i := offset; i < offset+50 && i <= 51; i++ {
					n := anycastObservationFixture()
					n["id"] = fmt.Sprintf("gateway-%d", i)
					n["ipPoolName"] = fmt.Sprintf("Pool-%d", i)
					n["vlanName"] = fmt.Sprintf("VLAN-%d", i)
					n["vlanId"] = i + 1
					if i == 51 {
						switch mode {
						case "duplicate-pool":
							n["ipPoolName"] = "Pool-1"
						case "duplicate-vlan":
							n["vlanId"] = 2
						case "duplicate-name":
							n["vlanName"] = "VLAN-1"
						case "other-fabric":
							n["fabricId"] = "zone"
							n["vlanId"] = 2
							n["vlanName"] = "VLAN-1"
							n["ipPoolName"] = "Pool-1"
						}
					}
					rows = append(rows, n)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": rows})
			})
			got, err := c.anycastGateways(context.Background())
			valid := mode == "paged" || mode == "other-fabric"
			if (err == nil) != valid {
				t.Fatal("unexpected inventory result", err)
			}
			if valid && (len(got) != 51 || calls != 2) {
				t.Fatal("inventory truncated")
			}
			if !valid && got != nil {
				t.Fatal("partial inventory returned")
			}
		})
	}
}
