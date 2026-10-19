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

// Keep the serialized normalized shape available to read-only live round-trip fixtures.
func authTemplateModel(p authProfile) map[string]any {
	m := map[string]any{"name": p.Name, "authentication_order": *p.Order, "dot1x_to_mab_fallback_timeout": *p.Timeout, "wake_on_lan": *p.Wake, "number_of_hosts": *p.Hosts}
	if p.Name == "Low Impact" && p.ACL != nil {
		raw, _ := json.Marshal(p.ACL.Contracts)
		var contracts []any
		_ = json.Unmarshal(raw, &contracts)
		m["pre_auth_acl"] = map[string]any{"enabled": *p.ACL.Enabled, "implicit_action": *p.ACL.ImplicitAction, "access_contracts": contracts}
	}
	return m
}

const authModel = `{"catalyst_center":{"authentication_templates":[{"name":"Closed Authentication","authentication_order":"dot1x","dot1x_to_mab_fallback_timeout":10,"wake_on_lan":false,"number_of_hosts":"Single"}]}}`

func TestAuthTemplateReport(t *testing.T) {
	for _, mode := range []string{"match", "drift", "false", "missing", "partial", "fabric", "missing-field", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			want, err := parseAuthTemplates([]byte(authModel))
			if err != nil {
				t.Fatal(err)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != authProfilesPath || r.URL.Query().Get("isGlobalAuthenticationProfile") != "true" {
					t.Error("unexpected request")
				}
				p := want[0]
				p.ID = "profile"
				if mode == "drift" {
					x := "Unlimited"
					p.Hosts = &x
				}
				if mode == "false" {
					x := true
					p.Wake = &x
				}
				if mode == "fabric" {
					p.FabricID = "fabric"
				}
				if mode == "missing-field" {
					p.Wake = nil
				}
				items := []authProfile{p}
				if mode == "missing" {
					items = []authProfile{}
				}
				if mode == "duplicate" {
					q := p
					q.ID = "other"
					items = append(items, q)
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				json.NewEncoder(w).Encode(map[string]any{"response": items})
			})
			drift, err := c.reportAuthTemplates(context.Background(), want)
			valid := mode == "match" || mode == "drift" || mode == "false"
			if (err == nil) != valid {
				t.Fatalf("valid=%v err=%v", valid, err)
			}
			if !valid {
				return
			}
			n := 0
			if mode != "match" {
				n = 1
			}
			if len(drift) != n {
				t.Fatalf("drift=%v", drift)
			}
		})
	}
}
func TestAuthTemplateValidation(t *testing.T) {
	for _, raw := range []string{strings.Replace(authModel, `"wake_on_lan":false`, `"wake_on_lan":null`, 1), strings.Replace(authModel, `"number_of_hosts":"Single"`, `"number_of_hosts":"invalid"`, 1), strings.Replace(authModel, `"wake_on_lan":false`, `"wake_on_lan":false,"bpdu_guard":true`, 1), strings.Replace(authModel, `"Closed Authentication"`, `"Low Impact"`, 1)} {
		if _, err := parseAuthTemplates([]byte(raw)); err == nil {
			t.Fatal("invalid or unresolved global profile accepted")
		}
	}
	low := strings.Replace(authModel, `"Closed Authentication"`, `"Low Impact"`, 1)
	low = strings.Replace(low, `"number_of_hosts":"Single"`, `"number_of_hosts":"Single","pre_auth_acl":{"enabled":true,"implicit_action":"DENY","description":"ignored upstream","access_contracts":[{"action":"PERMIT","protocol":"UDP","port":"domain"}]}`, 1)
	profiles, err := parseAuthTemplates([]byte(low))
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"authentication_templates": []any{authTemplateModel(profiles[0])}}})
	if strings.Contains(string(raw), "ignored upstream") {
		t.Fatal("ignored description survived normalization")
	}
	if _, err := parseAuthTemplates(raw); err != nil {
		t.Fatal(err)
	}
}
