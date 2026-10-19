// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"encoding/json"
	"strings"
	"testing"
)

func globalSiteCredentialModel(value any) []byte {
	site := map[string]any{"name": "Global"}
	for _, k := range globalCredentialKinds {
		site[k.model] = value
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{}, "sites": map[string]any{"areas": []any{site}}}})
	return raw
}

func TestGlobalSiteCredentialParsing(t *testing.T) {
	intent, err := parseNetworkReportIntent(globalSiteCredentialModel("example"))
	if err != nil || len(intent.assignments) != 6 {
		t.Fatal("global assignments", err)
	}
	for _, a := range intent.assignments {
		if a.site != "Global" || a.siteKind != "global" {
			t.Fatal("Global treated as area", a)
		}
	}
	for _, raw := range []string{
		strings.Replace(string(globalSiteCredentialModel("example")), `"name":"Global"`, `"name":"Other"`, 1),
		strings.Replace(string(globalSiteCredentialModel("example")), `"name":"Global"`, `"name":"Global","parent_name":null`, 1),
		strings.Replace(string(globalSiteCredentialModel("example")), `"name":"Global"`, `"name":"Global","ip_pools_reservations":[]`, 1),
		strings.Replace(string(globalSiteCredentialModel("example")), `"areas"`, `"buildings"`, 1),
	} {
		if _, err := parseNetworkReportIntent([]byte(raw)); err == nil {
			t.Fatal("invalid Global reference accepted")
		}
	}
	// Root handling must not leak into the next entry of the same collection.
	raw := []byte(`{"catalyst_center":{"network_settings":{},"sites":{"areas":[{"name":"Global","cli_credentials":"one"},{"name":"Lab","parent_name":"Global","cli_credentials":"two"}]}}}`)
	intent, err = parseNetworkReportIntent(raw)
	if err != nil || len(intent.assignments) != 2 {
		t.Fatal(err)
	}
	for _, a := range intent.assignments {
		if a.site == "Global/Lab" && a.siteKind != "area" {
			t.Fatal("root kind leaked into child")
		}
	}
}

func TestGlobalCredentialAndProfileBinding(t *testing.T) {
	raw := `{"catalyst_center":{"network_settings":{"network":[{"name":"base","ntp_servers":["192.0.2.1"]}]},"sites":{"areas":[{"name":"Global","cli_credentials":"example","network_settings":{"network":"base"}}]}}}`
	intent, err := parseNetworkReportIntent([]byte(raw))
	if err != nil || len(intent.assignments) != 1 || len(intent.bindings) != 1 || intent.assignments[0].site != "Global" {
		t.Fatal("combined root model", err)
	}
	duplicate := strings.Replace(raw, `"sites":{`, `"sites":{"global":{"network_settings":{"network":"base"}},`, 1)
	for range 20 {
		if _, err := parseNetworkReportIntent([]byte(duplicate)); err == nil {
			t.Fatal("duplicate root profiles accepted")
		}
	}
}
