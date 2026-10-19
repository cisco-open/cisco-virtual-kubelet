// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestFabricPortAssignmentVLANs(t *testing.T) {
	for _, mode := range []string{"l2", "gateway", "both", "omitted", "missing", "foreign", "wrong-traffic", "conflicting-vlan", "duplicate-gateway", "same-data-voice", "gateway-replaced"} {
		t.Run(mode, func(t *testing.T) {
			port, name := "GigabitEthernet1/0/1", "DATA"
			want := fabricPortAssignmentIntent{Interface: &port, Type: "USER_DEVICE", DataVLAN: &name}
			flag := false
			l2 := map[fabricL2Key]fabricL2Network{{"fabric", "DATA"}: {ID: "l2-id", FabricID: "fabric", VLANName: "DATA", VLAN: 100, Traffic: "DATA", Wireless: &flag}}
			gateway, err := parseAnycastGatewayObservation(json.RawMessage(`{"id":"gateway-id","fabricId":"fabric","virtualNetworkName":"Users","ipPoolName":"pool","vlanName":"DATA","vlanId":100,"trafficType":"DATA"}`))
			if err != nil {
				t.Fatal(err)
			}
			gateways := map[anycastGatewayKey]anycastGateway{{"fabric", "pool"}: gateway}
			switch mode {
			case "l2":
				gateways = nil
			case "gateway", "gateway-replaced":
				l2 = nil
			case "omitted":
				want.DataVLAN = nil
			case "missing":
				l2 = nil
				gateways = nil
			case "foreign":
				l2 = nil
				gateway.FabricID = "foreign"
				gateways[anycastGatewayKey{"fabric", "pool"}] = gateway
			case "wrong-traffic":
				gateway.fields["trafficType"] = "VOICE"
			case "conflicting-vlan":
				gateway.VLAN = 200
				gateways[anycastGatewayKey{"fabric", "pool"}] = gateway
			case "duplicate-gateway":
				gateways[anycastGatewayKey{"fabric", "second"}] = gateway
			case "same-data-voice":
				want.VoiceVLAN = &name
			}
			got, err := resolveFabricPortAssignmentVLANs("fabric", []fabricPortAssignmentIntent{want}, l2, gateways)
			valid := mode == "l2" || mode == "gateway" || mode == "both" || mode == "omitted" || mode == "gateway-replaced"
			if (err == nil) != valid {
				t.Fatal("resolution outcome", err)
			}
			if !valid {
				return
			}
			if mode == "omitted" {
				if len(got) != 0 {
					t.Fatal("invented prerequisite")
				}
				return
			}
			if len(got) != 1 || got[0].Name != "DATA" || got[0].Traffic != "DATA" || got[0].VLAN != 100 {
				t.Fatal("incomplete binding", got)
			}
			if (got[0].L2 != nil) != (mode == "l2" || mode == "both") || (got[0].Gateway != nil) != (mode != "l2") {
				t.Fatal("lost native view")
			}
			if mode == "gateway-replaced" {
				before := string(got[0].Gateway)
				gateway.fields["id"] = "replacement"
				gateway.ID = "replacement"
				gateways[anycastGatewayKey{"fabric", "pool"}] = gateway
				after, err := resolveFabricPortAssignmentVLANs("fabric", []fabricPortAssignmentIntent{want}, l2, gateways)
				if err != nil || before == string(after[0].Gateway) {
					t.Fatal("replacement lost from evidence", err)
				}
			}
		})
	}
}

func TestFabricPortAssignmentVLANReaders(t *testing.T) {
	for _, mode := range []string{"valid", "omitted", "partial"} {
		t.Run(mode, func(t *testing.T) {
			reads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if r.Method != "GET" {
					t.Error("dependency mutation")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case fabricL2NetworksPath:
					fmt.Fprint(w, `{"response":[]}`)
				case anycastGatewaysPath:
					if mode == "partial" {
						fmt.Fprint(w, `{"response":{}}`)
						return
					}
					fmt.Fprint(w, `{"response":[{"id":"gateway-id","fabricId":"fabric","virtualNetworkName":"Users","ipPoolName":"pool","vlanName":"DATA","vlanId":100,"trafficType":"DATA"}]}`)
				default:
					t.Error("unexpected endpoint", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			port, name := "GigabitEthernet1/0/1", "DATA"
			want := fabricPortAssignmentIntent{Interface: &port, Type: "USER_DEVICE", DataVLAN: &name}
			if mode == "omitted" {
				want.DataVLAN = nil
			}
			got, err := c.fabricPortAssignmentVLANs(context.Background(), "fabric", []fabricPortAssignmentIntent{want})
			if mode == "partial" {
				if err == nil || got != nil {
					t.Fatal("incomplete reference inventory accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "omitted" {
				if reads != 0 || len(got) != 0 {
					t.Fatal("omission caused extra reads")
				}
			} else if reads != 2 || len(got) != 1 {
				t.Fatal("incomplete native reference read")
			}
		})
	}
}
