//go:build live

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestWirelessMetadataQualificationGuard(t *testing.T) {
	for _, tc := range []struct {
		name, resource, action, target, method, path, body string
		allowed                                            bool
	}{
		{"ap-create", "ap-profile:CVK_A", "Create", "", "POST", wirelessAPPath, `{"apProfileName":"CVK_A"}`, true},
		{"power-create", "power-profile:CVK_P", "Create", "", "POST", wirelessPowerPath, `{"profileName":"CVK_P"}`, true},
		{"open-create", "ssid:global:CVK_O", "Create", "", "POST", wirelessSSIDPath("global"), `{"ssid":"CVK_O","authType":"OPEN","isEnabled":false}`, true},
		{"open-update", "ssid:global:CVK_O", "Update", "original", "PUT", wirelessSSIDPath("global") + "/original", `{"ssid":"CVK_O","authType":"OPEN","isEnabled":false}`, true},
		{"wrong-ap-name", "ap-profile:CVK_A", "Create", "", "POST", wirelessAPPath, `{"apProfileName":"unrelated"}`, false},
		{"wrong-power-name", "power-profile:CVK_P", "Create", "", "POST", wirelessPowerPath, `{"profileName":"unrelated"}`, false},
		{"wrong-ssid-uuid", "ssid:global:CVK_O", "Update", "original", "PUT", wirelessSSIDPath("global") + "/replacement", `{"ssid":"CVK_O","authType":"OPEN","isEnabled":false}`, false},
		{"enabled-ssid", "ssid:global:CVK_O", "Create", "", "POST", wirelessSSIDPath("global"), `{"ssid":"CVK_O","authType":"OPEN","isEnabled":true}`, false},
		{"credential-ssid", "ssid:global:CVK_O", "Create", "", "POST", wirelessSSIDPath("global"), `{"ssid":"CVK_O","authType":"OPEN","isEnabled":false,"passphrase":"never-forward"}`, false},
		{"wrong-auth", "ssid:global:CVK_O", "Create", "", "POST", wirelessSSIDPath("global"), `{"ssid":"CVK_O","authType":"WPA2_PERSONAL","isEnabled":false}`, false},
		{"device-provision", "ap-profile:CVK_A", "Create", "", "POST", "/dna/intent/api/v1/wireless/ap-provision", `{"apProfileName":"CVK_A"}`, false},
		{"unknown-delete", "", "", "", "DELETE", wirelessAPPath + "/unrelated", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			g := &wirelessMetadataTransport{resource: tc.resource, action: tc.action, target: tc.target, cleanup: func(string) bool { return false }, deleted: map[string]bool{}, base: metadataSubmitRoundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
			})}
			req, err := http.NewRequest(tc.method, "https://fixture.invalid"+tc.path, strings.NewReader(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			response, err := g.RoundTrip(req)
			if (err == nil) != tc.allowed || calls != map[bool]int{false: 0, true: 1}[tc.allowed] {
				t.Fatal("unexpected mutation escaped qualification scope", err, calls)
			}
			if response != nil {
				response.Body.Close()
			}
			if tc.allowed {
				if _, err := g.RoundTrip(req); err == nil || calls != 1 {
					t.Fatal("one-use qualification scope replayed")
				}
			}
		})
	}
}
