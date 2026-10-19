// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func TestGlobalCredentialReporting(t *testing.T) {
	for _, kind := range globalCredentialKinds {
		t.Run(kind.model, func(t *testing.T) {
			want, _ := globalCredentialFixture(t, kind.model)
			var entry map[string]any
			_ = json.Unmarshal([]byte(globalCredentialFixtureEntries[kind.model]), &entry)
			for key := range entry {
				if strings.HasSuffix(key, "_version") {
					delete(entry, key)
				}
			}
			encoded, _ := json.Marshal(entry)
			raw := globalCredentialModel(kind.model, string(encoded))
			metadata := globalCredentialMetadataPayload(want.metadata)
			metadata["id"] = "credential-id"
			for _, leaf := range want.credentials {
				metadata[leaf.field] = "private-value"
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != globalCredentialsPath {
					t.Error("report mutated or read unrelated resource")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{kind.api: []any{metadata}}})
			})
			plans, err := c.compileConfigReports(raw, []string{"network_settings"})
			if err != nil || len(plans) != 1 || plans[0].count != 1 || !strings.Contains(plans[0].note, "values are not compared") {
				t.Fatal("metadata report compile failed", err)
			}
			if drift, err := plans[0].observe(context.Background()); err != nil || len(drift) != 0 {
				t.Fatal("matching metadata drift", err)
			}
			metadata["description"] = "other"
			if drift, err := plans[0].observe(context.Background()); err != nil || len(drift) != 1 || string(drift[0].Change) != "Add" {
				t.Fatal("missing credential not reported", err)
			}
			if _, err := c.compileConfigurationExecutor(raw, "network_settings"); err == nil {
				t.Fatal("metadata-only apply bypassed version ownership")
			}
			if _, err := c.compileConfigReports(globalCredentialModel(kind.model, globalCredentialFixtureEntries[kind.model]), []string{"network_settings"}); err == nil {
				t.Fatal("report accepted credential version intent")
			}
		})
	}
}

func TestGlobalCredentialReportCoexistsWithNetworkProfiles(t *testing.T) {
	raw := []byte(strings.Replace(networkReportModel, `"network_settings":{"network":[`, `"network_settings":{"device_credentials":{"https_read_credentials":[{"name":"fixture","username":"tester","port":443}]},"network":[`, 1))
	intent, err := parseNetworkReportIntent(raw)
	if err != nil || len(intent.credentials) != 1 || len(intent.bindings) != 1 {
		t.Fatal("combined metadata report discarded a component", err)
	}
	if _, err := (&client{}).compileConfigurationExecutor(raw, "network_settings"); err == nil {
		t.Fatal("combined metadata input bypassed credential apply gate")
	}
}
