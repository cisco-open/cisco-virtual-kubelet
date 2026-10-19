// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

func siteLocalConfigurationFixture(path, id string) (map[string]any, bool) {
	groups := []string{}
	for _, kind := range []string{"ntp", "dns", "dhcp", "timeZone", "banner", "telemetry", "aaa"} {
		if path != sitesPath+"/"+id+"/"+kind+"Settings" {
			continue
		}
		groups = []string{kind}
		if kind == "telemetry" {
			groups = telemetryGroups
		}
		if kind == "aaa" {
			groups = nil
			for _, group := range aaaGroups {
				groups = append(groups, group.api)
			}
		}
	}
	if path == siteCredentialsPath(id) {
		for _, kind := range globalCredentialKinds {
			groups = append(groups, credentialSlot(kind.model))
		}
	}
	if len(groups) == 0 {
		return nil, false
	}
	response := map[string]any{}
	for _, group := range groups {
		response[group] = nil
	}
	return response, true
}

func serveEmptySiteRetirementConsumers(t *testing.T, w http.ResponseWriter, r *http.Request, id string) bool {
	t.Helper()
	if r.URL.Path == lanSessionStatusPath {
		if r.Method != http.MethodGet || r.URL.Query().Get("offset") != "1" || r.URL.Query().Get("limit") != "10" {
			t.Error("LAN scope inventory escaped complete pagination")
			w.WriteHeader(500)
			return true
		}
		fmt.Fprint(w, `{"response":[]}`)
		return true
	}
	response, known := siteLocalConfigurationFixture(r.URL.Path, id)
	if !known {
		return false
	}
	if r.Method != http.MethodGet || r.URL.Query().Get("_inherited") != "false" {
		t.Error("local configuration read escaped exact local-only scope")
		w.WriteHeader(500)
		return true
	}
	json.NewEncoder(w).Encode(map[string]any{"response": response})
	return true
}

func TestSiteRetirementLocalConfiguration(t *testing.T) {
	for _, kind := range []string{"ntp", "dns", "dhcp", "timeZone", "banner", "telemetry", "aaa", "credentials"} {
		for _, mode := range []string{"inherited", "empty", "configured", "unknown-group", "missing-group", "unavailable", "partial", "invalid"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				writes, reads := 0, 0
				endpoint := sitesPath + "/" + siteOwnedTestUUID + "/" + kind + "Settings"
				if kind == "credentials" {
					endpoint = siteCredentialsPath(siteOwnedTestUUID)
				}
				c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == authPath {
						fmt.Fprint(w, `{"Token":"test"}`)
						return
					}
					if r.Method != http.MethodGet {
						writes++
						w.WriteHeader(500)
						return
					}
					response, known := siteLocalConfigurationFixture(r.URL.Path, siteOwnedTestUUID)
					if !known || r.URL.Query().Get("_inherited") != "false" {
						t.Error("unexpected local-only read")
						w.WriteHeader(500)
						return
					}
					reads++
					if r.URL.Path == endpoint {
						first := ""
						for group := range response {
							first = group
							break
						}
						switch mode {
						case "empty":
							response[first] = map[string]any{}
						case "configured":
							response[first] = map[string]any{"private-fixture-setting": "private-fixture-value"}
						case "unknown-group":
							response["futureSetting"] = nil
						case "missing-group":
							delete(response, first)
						case "unavailable":
							w.WriteHeader(503)
							fmt.Fprint(w, "private-fixture-value")
							return
						case "partial":
							w.WriteHeader(206)
						case "invalid":
							fmt.Fprint(w, `{"response":[]}`)
							return
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": response})
				})
				snapshot, err := c.siteEmptyLocalConfiguration(context.Background(), siteOwnedTestUUID)
				allowed := mode == "inherited" || mode == "empty" && kind != "credentials"
				if (err == nil) != allowed || writes != 0 {
					t.Fatal("local policy absence mismatch", err, writes)
				}
				if allowed && (len(snapshot) != 8 || reads != 8) {
					t.Fatal("incomplete local settings/credential snapshot")
				}
				if err != nil && strings.Contains(err.Error(), "private-fixture-value") {
					t.Fatal("local policy value leaked through error")
				}
			})
		}
	}
}
