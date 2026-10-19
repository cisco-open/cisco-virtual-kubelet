// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func TestCombinedSystemSettingsPreflightBeforeWrites(t *testing.T) {
	for _, failure := range []string{"flag-read", "attribute-read", "server-read"} {
		t.Run(failure, func(t *testing.T) {
			a, config, source, _ := credentialVersionJournalFixture(t)
			nc, err := a.currentController(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			a.intentSecretPath = source.secrets.root
			source.raw = []byte(strings.Replace(policyServerApplyModel, `"system_settings":{`, `"system_settings":{"external_authentication":{"enabled":false,"aaa_attribute":"CVK-Role"},`, 1))
			writes := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					writes++
					w.WriteHeader(500)
					return
				}
				switch r.URL.Path {
				case externalAuthPath:
					if failure == "flag-read" {
						w.WriteHeader(500)
						return
					}
					fmt.Fprint(w, `{"response":{"external-authentication-flag":[{"enabled":false}]}}`)
				case externalAAAAttributePath:
					if failure == "attribute-read" {
						w.WriteHeader(500)
						return
					}
					fmt.Fprint(w, `{"response":{"aaa-attributes":[{"attributeName":""}]}}`)
				case authenticationPolicyServersPath:
					if failure == "server-read" {
						w.WriteHeader(500)
						return
					}
					fmt.Fprint(w, `{"response":[]}`)
				default:
					t.Error("unexpected read")
					w.WriteHeader(404)
				}
			})
			plan, err := a.compilePolicyServerApply(nc, config, source)
			if err != nil || plan.executor.count() != 3 {
				t.Fatal("combined plan failed", err)
			}
			if write, err := plan.executor.next(context.Background()); err == nil || write != nil || writes != 0 {
				t.Fatal("partial observation produced a write")
			}
		})
	}
}

func TestCombinedSystemSettingsDoesNotDropUnknownIntent(t *testing.T) {
	for _, field := range []string{`"unknown":true`, `"external_authentication":{"enabled":"true"}`, `"external_authentication":{"enabled":false,"unknown":1}`} {
		raw := []byte(strings.Replace(policyServerApplyModel, `"system_settings":{`, `"system_settings":{`+field+`,`, 1))
		if _, _, err := splitSystemSettingsApply(raw); err == nil {
			t.Fatal("unknown or invalid combined intent silently discarded")
		}
	}
}

func TestCombinedSystemSettingsCredentialRoutingSurvivesPruning(t *testing.T) {
	e := &systemSettingsExecutor{}
	if !e.credentialOperation(&ciskov1.NetworkControllerConfigurationOperation{Resource: "192.0.2.43"}) {
		t.Fatal("pruned server binding bypassed credential verification")
	}
	if e.credentialOperation(&ciskov1.NetworkControllerConfigurationOperation{Resource: "external_authentication"}) {
		t.Fatal("singleton incorrectly requires a credential receipt")
	}
	if _, err := e.verify(context.Background(), &ciskov1.NetworkControllerConfigurationOperation{Resource: "external_authentication", CredentialVersions: []ciskov1.NetworkControllerConfigurationCredentialVersion{{Version: 1}}}); err == nil {
		t.Fatal("credential operation accepted as a singleton")
	}
}
