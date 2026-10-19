// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

func TestNetworkCredentialDependencyPreflight(t *testing.T) {
	for _, mode := range []string{"declared-pending", "undeclared-pending", "missing-site", "incomplete-site-read"} {
		t.Run(mode, func(t *testing.T) {
			want, secrets := globalCredentialFixture(t, "cli_credentials")
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("preflight mutated controller")
					w.WriteHeader(400)
					return
				}
				switch r.URL.Path {
				case globalCredentialsPath:
					fmt.Fprint(w, `{"response":{}}`)
				case sitesPath:
					sites := sampleSites()
					if mode == "missing-site" {
						sites = sites[:1]
					}
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case siteCredentialsPath("area"):
					slots := map[string]any{}
					if mode != "incomplete-site-read" {
						for _, kind := range globalCredentialKinds {
							slots[credentialSlot(kind.model)] = nil
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": slots})
				default:
					t.Errorf("unexpected request %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			name := want.metadata.Name
			if mode == "undeclared-pending" {
				name = "external"
			}
			intent := siteCredentialIntent{site: "Global/Lab", siteKind: "area", kind: "cli_credentials", name: name}
			assignments := &siteCredentialExecutor{client: c, desired: []siteCredentialIntent{intent}}
			e := &networkCredentialConfigurationExecutor{
				credentials: &globalCredentialExecutor{client: c, desired: []globalCredentialIntent{want}, secrets: secrets, owner: "owner"},
				network:     &networkSectionExecutor{settings: &networkConfigurationExecutor{client: c}, ipam: &ipamConfigurationExecutor{client: c}, assignments: assignments},
			}
			write, err := e.next(context.Background())
			if mode == "declared-pending" {
				if err != nil || write == nil || write.resource != want.resource() {
					t.Fatal("declared dependency was not scheduled first", err)
				}
				// The relaxed preflight is not an assignment executor bypass.
				if write, err := assignments.next(context.Background()); err == nil || write != nil {
					t.Fatal("assignment submitted a placeholder credential")
				}
			} else if err == nil || write != nil {
				t.Fatal("credential write escaped incomplete dependency preflight")
			}
		})
	}
}

func TestNetworkCredentialProjectionPreservesUnsupportedIntent(t *testing.T) {
	for _, extra := range []string{`"fabric":{}`, `"sites":null`, `"sites":{"areas":[{"name":"Lab","parent_name":"Global","cli_credentials":null}]}`} {
		raw := []byte(`{"catalyst_center":{"network_settings":{"device_credentials":{"cli_credentials":[` + globalCredentialFixtureEntries["cli_credentials"] + `]}},` + extra + `}}`)
		_, rest, err := splitNetworkCredentialConfiguration(raw)
		if err == nil {
			_, err = (&client{}).compileConfigurationExecutor(rest, "network_settings")
		}
		if err == nil {
			t.Fatal("unsupported intent silently discarded", extra)
		}
	}
}
