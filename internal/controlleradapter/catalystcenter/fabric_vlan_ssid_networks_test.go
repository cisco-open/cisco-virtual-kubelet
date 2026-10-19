// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import "testing"

func TestFabricVLANSSIDNetworks(t *testing.T) {
	for _, mode := range []string{"l2", "gateway", "both", "missing", "foreign", "disabled-l2", "unknown-l2", "disabled-gateway", "unknown-gateway", "conflict", "voice", "duplicate-gateway"} {
		t.Run(mode, func(t *testing.T) {
			wireless := true
			l2 := map[fabricL2Key]fabricL2Network{{"fabric", "Clients"}: {ID: "l2", FabricID: "fabric", VLANName: "Clients", VLAN: 100, Traffic: "DATA", Wireless: &wireless}}
			gateway := anycastGateway{ID: "gateway", FabricID: "fabric", VLANName: "Clients", VLAN: 100, fields: map[string]any{"id": "gateway", "fabricId": "fabric", "vlanName": "Clients", "vlanId": 100, "trafficType": "DATA", "isWirelessPool": true}}
			gateways := map[anycastGatewayKey]anycastGateway{}
			switch mode {
			case "gateway":
				l2 = nil
				gateways[anycastGatewayKey{"fabric", "pool"}] = gateway
			case "both":
				gateways[anycastGatewayKey{"fabric", "pool"}] = gateway
			case "missing":
				l2 = nil
			case "foreign":
				row := l2[fabricL2Key{"fabric", "Clients"}]
				row.FabricID = "other"
				l2 = map[fabricL2Key]fabricL2Network{{"other", "Clients"}: row}
			case "disabled-l2":
				wireless = false
			case "unknown-l2":
				row := l2[fabricL2Key{"fabric", "Clients"}]
				row.Wireless = nil
				l2[fabricL2Key{"fabric", "Clients"}] = row
			case "disabled-gateway", "unknown-gateway":
				if mode == "disabled-gateway" {
					gateway.fields["isWirelessPool"] = false
				} else {
					delete(gateway.fields, "isWirelessPool")
				}
				gateways[anycastGatewayKey{"fabric", "pool"}] = gateway
			case "conflict":
				gateway.VLAN = 200
				gateways[anycastGatewayKey{"fabric", "pool"}] = gateway
			case "voice":
				row := l2[fabricL2Key{"fabric", "Clients"}]
				row.Traffic = "VOICE"
				l2[fabricL2Key{"fabric", "Clients"}] = row
			case "duplicate-gateway":
				gateways[anycastGatewayKey{"fabric", "one"}] = gateway
				gateways[anycastGatewayKey{"fabric", "two"}] = gateway
			}
			got, err := resolveFabricVLANSSIDNetworks("fabric", []fabricVLANSSID{{VLAN: "Clients", SSIDs: []fabricSSIDBinding{{Name: "Staff"}}}}, l2, gateways)
			valid := mode == "l2" || mode == "gateway" || mode == "both"
			if (err == nil) != valid {
				t.Fatal("wireless VLAN binding", err)
			}
			if !valid {
				if got != nil {
					t.Fatal("partial binding escaped")
				}
				return
			}
			if len(got) != 1 || got[0].VLAN != 100 || got[0].Traffic != "DATA" {
				t.Fatal("incorrect binding", got)
			}
			if mode == "both" && (got[0].L2 == nil || got[0].Gateway == nil) {
				t.Fatal("lost native evidence")
			}
		})
	}
}
