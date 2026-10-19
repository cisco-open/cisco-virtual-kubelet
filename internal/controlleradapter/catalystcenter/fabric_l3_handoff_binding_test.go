// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFabricL3IPHandoffBinding(t *testing.T) {
	for _, mode := range []string{"valid", "pool", "wrong-device", "not-border", "not-layer3", "missing-port", "virtual-port", "missing-transit", "sda-transit", "bad-asn", "missing-network", "unknown-membership", "wrong-fabric", "anchored-network", "duplicate-fabric"} {
		t.Run(mode, func(t *testing.T) {
			var value any
			if err := json.Unmarshal([]byte(fabricL3IPHandoffModel), &value); err != nil {
				t.Fatal(err)
			}
			intents, err := parseFabricL3IPHandoffs("border", "", value)
			if err != nil {
				t.Fatal(err)
			}
			want := intents[0]
			flag := true
			device := fabricDeviceConfiguration{ID: "membership", NetworkDeviceID: "device", FabricID: "fabric", Roles: []string{"BORDER_NODE"}, Border: &fabricBorderSettings{Types: []string{"LAYER_3"}, Layer3: &fabricBorderLayer3{ASN: "65000", DefaultExit: &flag, ImportExternal: &flag}}}
			ports := []lanLinkInterface{{ID: "port", DeviceID: "device", PortName: want.port, Type: "Physical", Index: "1"}}
			transit := fabricTransit{ID: "transit", Name: "Transit", Type: "IP_BASED_TRANSIT", IP: &ipTransitSettings{Protocol: "BGP", ASN: "65001"}}
			network := fabricL3Network{ID: "network", Name: "Users", Fabrics: []string{"fabric"}, membershipsKnown: true}
			switch mode {
			case "pool":
				want.pool = "ExternalPool"
			case "wrong-device":
				want.device = "other"
			case "not-border":
				device.Roles = []string{"EDGE_NODE"}
			case "not-layer3":
				device.Border = &fabricBorderSettings{Types: []string{"LAYER_2"}}
			case "missing-port":
				ports = nil
			case "virtual-port":
				ports[0].Type = "Virtual"
			case "missing-transit":
				transit.Name = "Other"
			case "sda-transit":
				transit.Type = "SDA_LISP_PUB_SUB_TRANSIT"
			case "bad-asn":
				transit.IP.ASN = "0"
			case "missing-network":
				network.Name = "Other"
			case "unknown-membership":
				network.membershipsKnown = false
			case "wrong-fabric":
				network.Fabrics = []string{"other"}
			case "anchored-network":
				network.Anchor = "fabric"
			case "duplicate-fabric":
				network.Fabrics = []string{"fabric", "fabric"}
			}
			transits := map[string]fabricTransit{transit.Name: transit}
			networks := map[string]fabricL3Network{network.Name: network}
			binding, err := bindFabricL3IPHandoff(want, "border", device, ports, transits, networks)
			if mode != "valid" && mode != "pool" {
				if err == nil || binding.digest != "" {
					t.Fatal("unresolved dependency accepted")
				}
				return
			}
			if err != nil || binding.desired.TransitID != "transit" || binding.desired.Network != "Users" || binding.desired.MSS == nil || *binding.desired.MSS != 1400 {
				t.Fatal("incorrect binding", err)
			}
			raw, err := json.Marshal(binding.desired)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "pool" {
				if !strings.Contains(string(raw), `"externalConnectivityIpPoolName":"ExternalPool"`) || strings.Contains(string(raw), "IpAddress") || strings.Contains(string(raw), "Ipv6Address") || want.local4 == "" {
					t.Fatal("incorrect pool precedence", string(raw))
				}
			} else if binding.desired.Local4 != "192.0.2.1/30" || binding.desired.Peer6 != "2001:db8::2/64" {
				t.Fatal("host prefixes lost")
			}
			transit.IP.ASN = "65002"
			changed, err := bindFabricL3IPHandoff(want, "border", device, ports, transits, networks)
			if err != nil || changed.digest == binding.digest {
				t.Fatal("transit settings not bound", err)
			}
			*want.mss = 1300
			if *binding.desired.MSS != 1400 {
				t.Fatal("payload aliases mutable intent")
			}
		})
	}
}
