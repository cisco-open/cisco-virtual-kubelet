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

func TestAAANetworkPreflightRefusesPartialApply(t *testing.T) {
	for _, invalid := range []string{"aaa-ownership", "network-response", "aaa-registration"} {
		t.Run(invalid, func(t *testing.T) {
			aaa := aaaExecutorFixture(t)
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.Method != http.MethodGet:
					writes++
					t.Error("premature mutation")
					w.WriteHeader(500)
				case r.URL.Path == authenticationPolicyServersPath:
					if invalid == "aaa-registration" {
						fmt.Fprint(w, `{"response":[]}`)
					} else {
						fmt.Fprint(w, aaaRegistrationFixture)
					}
				case r.URL.Path == sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case strings.HasSuffix(r.URL.Path, "/ntpSettings"):
					if invalid == "network-response" {
						fmt.Fprint(w, `{"response":{}}`)
					} else {
						fmt.Fprint(w, `{"response":{"ntp":null}}`)
					}
				case strings.HasSuffix(r.URL.Path, "/aaaSettings"):
					if invalid == "aaa-ownership" {
						fmt.Fprint(w, `{"response":`+aaaResponse+`}`)
					} else {
						fmt.Fprint(w, `{"response":{"aaaNetwork":null,"aaaClient":null}}`)
					}
				default:
					t.Errorf("unexpected API %s", r.URL.Path)
					w.WriteHeader(404)
				}
			})
			aaa.client = c
			e := &aaaNetworkConfigurationExecutor{aaa: aaa, settings: &networkConfigurationExecutor{client: c, bindings: []networkBinding{{path: "Global", kind: "global", settings: []networkSetting{{kind: "ntp", servers: []string{"192.0.2.1"}}}}}}}
			if next, err := e.next(context.Background()); err == nil || next != nil || writes != 0 {
				t.Fatal("incomplete model released a write")
			}
		})
	}
}

func TestAAANetworkBindingSplitPreservesSharedProfile(t *testing.T) {
	raw := string(aaaApplyModel())
	raw = strings.Replace(raw, `"global":{"network_settings":{"aaa_servers":"base"}}`, `"global":{"network_settings":{"aaa_servers":"base","network":"n"}},"areas":[{"name":"Lab","parent_name":"Global","network_settings":{"aaa_servers":"base"}}]`, 1)
	raw = strings.Replace(raw, `"aaa_servers":[`, `"network":[{"name":"n","ntp_servers":["192.0.2.1"]}],"aaa_servers":[`, 1)
	bindings, err := parseAAASettingsApply([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	aaa, other := splitAAASettingsBindings(bindings)
	if len(aaa) != 2 || len(other) != 1 || other[0].path != "Global" || len(bindings[0].settings) != 2 {
		t.Fatal("split lost or changed bindings")
	}
	for _, b := range aaa {
		for _, leaf := range b.settings[0].aaaCredentials {
			if !strings.HasPrefix(leaf.path, "/aaa_servers/0/") {
				t.Fatal("shared profile secret path was rewritten")
			}
		}
	}
}
