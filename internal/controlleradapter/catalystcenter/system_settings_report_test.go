// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const policyServerReportModel = `{"catalyst_center":{"system_settings":{"authentication_and_policy_servers":{"aaa":[{"ip_address":"192.0.2.43","protocols":{"radius":{"authentication_port":1812,"accounting_port":1813}},"retries":1,"timeout":5}]},"external_authentication":{"enabled":false}}}}`

func TestSystemSettingsPolicyServerReport(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("report attempted mutation")
		}
		switch r.URL.Path {
		case authenticationPolicyServersPath:
			fmt.Fprint(w, `{"response":[{"instanceUuid":"server","ipAddress":"192.0.2.43","isIseEnabled":false,"role":"primary","protocol":"RADIUS","authenticationPort":1812,"accountingPort":1813,"retries":1,"timeoutSeconds":5,"sharedSecret":"ignored-secret"}]}`)
		case externalAuthPath:
			fmt.Fprint(w, `{"response":{"external-authentication-flag":[{"enabled":false}]}}`)
		default:
			t.Error("unexpected API")
			w.WriteHeader(404)
		}
	})
	plans, err := c.compileConfigReports([]byte(policyServerReportModel), []string{"system_settings"})
	if err != nil || len(plans) != 1 || plans[0].count != 2 {
		t.Fatal("invalid report plan", err)
	}
	if drift, err := plans[0].observe(context.Background()); err != nil || len(drift) != 0 {
		t.Fatal("metadata report drift", err)
	}
	if _, err := c.compileConfigurationExecutor([]byte(policyServerReportModel), "system_settings"); err == nil {
		t.Fatal("server manifest apply bypassed credential binding gate")
	}
}
func TestSystemSettingsPolicyServerIntentRejected(t *testing.T) {
	for _, entry := range []string{
		`{"ip_address":"192.0.2.43","protocols":{"radius":{}},"shared_secret":"must-not-print"}`,
		`{"ip_address":"192.0.2.43","protocols":{"radius":{}},"shared_secret_version":2}`,
		`{"ip_address":"192.0.2.43","protocols":{"radius":{}},"retries":null}`,
		`{"ip_address":"192.0.2.43","protocols":{"radius":{"authentication_port":1.5}}}`,
		`{"ip_address":"host.example","protocols":{"radius":{}}}`,
		`{"ip_address":"192.0.2.43","protocols":{"radius":{"enable_key_wrap":{}}}}`,
	} {
		raw := []byte(`{"catalyst_center":{"system_settings":{"authentication_and_policy_servers":{"aaa":[` + entry + `]}}}}`)
		_, err := parseSystemSettingsReport(raw)
		if err == nil || strings.Contains(err.Error(), "must-not-print") {
			t.Fatal("unsupported or secret-bearing intent accepted/leaked")
		}
	}
}
