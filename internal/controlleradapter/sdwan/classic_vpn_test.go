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
	if _, e = Compile([]byte(strings.Replace(string(raw), "vpn_id: 10", "ipv4_primary_dns_server: 192.0.2.1", 1))); e == nil {
		t.Fatal("unmapped DNS branch accepted")
	}
}
func TestLiveClassicVPNLifecycle(t *testing.T) { liveParcelLifecycle(t, classicVPNFixture(t)) }
