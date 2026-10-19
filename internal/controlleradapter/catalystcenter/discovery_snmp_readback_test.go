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

// Exercise the actual inventory decoder and retained public-effect verifier
// with the wire shape documented by the API and observed in the lab.
func TestDiscoverySNMPReadback(t *testing.T) {
	for _, tc := range []struct {
		name, mode, user, version, wantMode string
		match                               bool
	}{
		{"noauth-camel", "NoAuthNoPriv", "operator", "", "NoAuthNoPriv", true},
		{"noauth-upper", "NOAUTHNOPRIV", "operator", "", "NoAuthNoPriv", true},
		{"auth-no-priv", "AUTHNOPRIV", "operator", "", "AuthNoPriv", true},
		{"auth-priv", "AUTHPRIV", "operator", "", "AuthPriv", true},
		{"explicit-v3", "NoAuthNoPriv", "operator", "v3", "NoAuthNoPriv", true},
		{"missing-mode", "", "operator", "", "NoAuthNoPriv", false},
		{"missing-user", "NoAuthNoPriv", "", "", "NoAuthNoPriv", false},
		{"unknown-mode", "FUTURE", "operator", "", "NoAuthNoPriv", false},
		{"contradictory-v2", "NoAuthNoPriv", "operator", "v2", "NoAuthNoPriv", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := map[string]any{"id": "243", "name": "fixture", "discoveryType": "Single", "ipAddressList": "192.0.2.1", "protocolOrder": "ssh", "snmpMode": tc.mode, "snmpUserName": tc.user}
			if tc.version != "" {
				body["snmpVersion"] = tc.version
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"fixture"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != discoveryPath+"/1/100" {
					t.Error("unexpected reader request", r.Method, r.URL.Path)
					w.WriteHeader(500)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"response": []any{body}})
			})
			rows, err := c.discoveries(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			got := rows["fixture"]
			wanted, err := discoveryCredentialEffect(map[string]any{"name": "fixture", "discoveryType": "SINGLE", "ipAddressList": "192.0.2.1", "protocolOrder": "SSH", "snmpVersion": "v3", "snmpMode": tc.wantMode, "snmpUserName": "operator"})
			if err != nil {
				t.Fatal(err)
			}
			actual, err := nativeDiscoveryCredentialEffect(got, wanted.mask)
			if (err == nil && actual == wanted) != tc.match {
				t.Fatal("native readback matched with missing/contradictory evidence, or valid v3 evidence was lost", got.SNMPVersion, got.SNMPMode, err)
			}
			if tc.version != "" && got.SNMPVersion != tc.version {
				t.Fatal("explicit native version overwritten")
			}
		})
	}
}
