// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"testing"
)

func TestAnycastGatewaySemanticReadback(t *testing.T) {
	for _, mode := range []string{"generated", "explicit", "critical-data", "critical-voice", "secondary", "wrong-secondary-binding", "missing-secondary", "missing-flag", "unknown", "wrong-generated-name", "wrong-declared-flag", "wrong-pool-binding", "inherited", "wrong-parent-vlan", "wrong-parent-name", "wrong-parent-pool", "infra"} {
		t.Run(mode, func(t *testing.T) {
			model := map[string]any{"l3_virtual_network": "Corp", "ip_pool_name": "access", "traffic_type": "DATA", "auto_generate_vlan_name": true, "critical_pool": false, "wireless_pool": false}
			observed := anycastObservationFixture()
			observed["ipPoolName"] = "access"
			observed["vlanName"] = "192.0.2.128/25-Corp"
			observed["isWirelessPool"] = false
			deps := anycastPoolDependencies{primary: fixtureReservations()[0], digest: "bound"}
			var parent *anycastGateway
			switch mode {
			case "explicit":
				model["auto_generate_vlan_name"] = false
				model["vlan_name"] = "Explicit"
				observed["vlanName"] = "Explicit"
			case "critical-data":
				model["critical_pool"] = true
				observed["isCriticalPool"] = true
				observed["vlanName"] = "CRITICAL_VLAN"
			case "critical-voice":
				model["critical_pool"] = true
				model["traffic_type"] = "VOICE"
				observed["isCriticalPool"] = true
				observed["trafficType"] = "VOICE"
				observed["vlanName"] = "VOICE_VLAN"
			case "secondary", "wrong-secondary-binding", "missing-secondary":
				model["additional_ip_pools"] = []any{"Extra"}
				if mode == "secondary" || mode == "wrong-secondary-binding" {
					observed["additionalIpPools"] = []any{map[string]any{"name": "Extra", "order": 2}}
				}
			case "missing-flag":
				delete(observed, "isWirelessPool")
			case "unknown":
				observed["newBehavior"] = true
			case "wrong-generated-name":
				observed["vlanName"] = "192.0.2.0/24-Corp"
			case "wrong-declared-flag":
				observed["isWirelessPool"] = true
			case "wrong-pool-binding":
				deps.primary.Name = "Other"
			case "infra":
				model["l3_virtual_network"] = "INFRA_VN"
				model["pool_type"] = "FABRIC_AP"
				delete(model, "critical_pool")
				delete(model, "wireless_pool")
				observed["virtualNetworkName"] = "INFRA_VN"
				observed["poolType"] = "FABRIC_AP"
				observed["vlanName"] = "192.0.2.128/25-INFRA_VN"
				delete(observed, "isCriticalPool")
				delete(observed, "isWirelessPool")
			}
			if mode == "secondary" || mode == "wrong-secondary-binding" || mode == "missing-secondary" {
				secondary := fixtureReservations()[0]
				secondary.ID = "extra"
				secondary.Name = "Extra"
				if mode == "wrong-secondary-binding" {
					secondary.Name = "Wrong"
				}
				deps.secondary = []ipReservation{secondary}
			}
			want, err := parseAnycastGateway(model)
			if err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(observed)
			got, err := parseAnycastGatewayObservation(raw)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "inherited", "wrong-parent-vlan", "wrong-parent-name", "wrong-parent-pool":
				p := got
				p.ID = "parent"
				p.FabricID = "parent-fabric"
				parent = &p
				if mode == "wrong-parent-vlan" {
					parent.VLAN = 151
				}
				if mode == "wrong-parent-name" {
					parent.VLANName = "other"
				}
				if mode == "wrong-parent-pool" {
					parent.Pool = "Other"
				}
			}
			match, err := anycastGatewayMatches(want, got, deps, parent)
			valid := mode == "generated" || mode == "explicit" || mode == "critical-data" || mode == "critical-voice" || mode == "secondary" || mode == "inherited" || mode == "infra"
			if match != valid || valid && err != nil {
				t.Fatal("unexpected semantic verification", match, err)
			}
			if (mode == "missing-secondary" || mode == "missing-flag" || mode == "unknown" || mode == "wrong-pool-binding" || mode == "wrong-parent-pool" || mode == "wrong-secondary-binding") && err == nil {
				t.Fatal("incomplete/stale evidence not distinguished from drift")
			}
		})
	}
}
