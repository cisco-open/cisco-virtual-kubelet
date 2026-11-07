// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"os"
	"strings"
	"testing"
)

func classicVPNDNSFixture(t *testing.T) []Resource {
	t.Helper()
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-vpn-dns.yaml")
	if err != nil {
		t.Fatal(err)
	}
	resources, err := Compile(raw)
	if err != nil {
		t.Fatal(err)
	}
	return resources
}

func TestClassicVPNDNSFixture(t *testing.T) {
	resources := classicVPNDNSFixture(t)
	if len(resources) != 1 {
		t.Fatal("VPN DNS resource missing")
	}
	definition := resources[0].Body["templateDefinition"].(map[string]any)
	for _, key := range []string{"dns", "dns-ipv6", "host"} {
		if definition[key].(map[string]any)["vipType"] != "constant" {
			t.Fatal("VPN DNS tree missing", key)
		}
	}
	if len(definition["host"].(map[string]any)["vipValue"].([]any)) != 2 {
		t.Fatal("DNS hosts did not flatten")
	}
	raw, _ := os.ReadFile("../../../examples/configs/sdwan/classic-vpn-dns.yaml")
	bad := strings.Replace(string(raw), "192.0.2.53", "bad-address", 1)
	if _, err := Compile([]byte(bad)); err == nil {
		t.Fatal("invalid DNS address accepted")
	}
}

func TestLiveClassicVPNDNSLifecycle(t *testing.T) { liveParcelLifecycle(t, classicVPNDNSFixture(t)) }
