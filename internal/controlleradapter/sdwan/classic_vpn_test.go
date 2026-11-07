// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"strings"
	"testing"
)

func classicVPNFixture(t *testing.T) []Resource {
	t.Helper()
	raw, e := os.ReadFile("../../../examples/configs/sdwan/classic-vpn.yaml")
	if e != nil {
		t.Fatal(e)
	}
	rs, e := Compile(raw)
	if e != nil {
		t.Fatal(e)
	}
	return rs
}
func TestClassicVPNFixture(t *testing.T) {
	rs := classicVPNFixture(t)
	if len(rs) != 1 || rs[0].Type != "cisco_vpn" {
		t.Fatal("invalid VPN fixture")
	}
	raw, e := os.ReadFile("../../../examples/configs/sdwan/classic-vpn.yaml")
	if e != nil {
		t.Fatal(e)
	}
	dns, e := Compile([]byte(strings.Replace(string(raw), "vpn_id: 10", "vpn_id: 10\n        ipv4_primary_dns_server: 192.0.2.1", 1)))
	if e != nil || dns[0].Body["templateDefinition"].(map[string]any)["dns"].(map[string]any)["vipType"] != "constant" {
		t.Fatal("VPN DNS branch did not compile", e)
	}
}
func TestLiveClassicVPNLifecycle(t *testing.T) { liveParcelLifecycle(t, classicVPNFixture(t)) }
