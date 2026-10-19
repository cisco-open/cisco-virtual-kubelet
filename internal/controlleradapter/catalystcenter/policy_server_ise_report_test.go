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

func iseReportModel() string {
	return strings.ReplaceAll(strings.ReplaceAll(policyISEModel, `"shared_secret_version":1,`, ""), `"password_version":1,`, "")
}
func TestISEMetadataAndIntegrationReport(t *testing.T) {
	for _, mode := range []string{"match", "missing", "metadata", "pending", "wrong-status-identity", "failed"} {
		t.Run(mode, func(t *testing.T) {
			intent, err := parseSystemSettingsReport([]byte(iseReportModel()))
			if err != nil || intent.count() != 2 {
				t.Fatal("ISE metadata did not compile", err)
			}
			calls := 0
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
					if mode == "missing" {
						fmt.Fprint(w, `{"response":[]}`)
						return
					}
					actual := policyServerMetadata(intent.servers[0])
					actual["instanceUuid"] = "ise-server"
					actual["sharedSecret"] = "private-value"
					dto := actual["ciscoIseDtos"].([]any)[0].(map[string]any)
					dto["password"] = "private-value"
					dto["subscriberName"] = "pxgrid_generated"
					if mode == "metadata" {
						dto["fqdn"] = "other.example.test"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": []any{actual}})
				case policyISEIntegrationPath:
					calls++
					id, status, step, failure := "ise-server", "COMPLETE", "COMPLETE", ""
					if mode == "wrong-status-identity" {
						id = "other-server"
					}
					if mode == "pending" {
						status, step = "INPROGRESS", "WAITING_USER_INPUT"
					}
					if mode == "failed" {
						status, step, failure = "FAILED", "FAILED", "private-value"
					}
					fmt.Fprintf(w, `{"aaaServerSettingId":%q,"overallStatus":%q,"overallErrorMessage":%q,"steps":[{"stepId":"1","stepStatus":%q,"certAcceptedByUser":false}]}`, id, status, failure, step)
				default:
					t.Error("unexpected API path")
					w.WriteHeader(404)
				}
			})
			plans, err := c.compileConfigReports([]byte(iseReportModel()), []string{"system_settings"})
			if err != nil || len(plans) != 1 || plans[0].count != 2 {
				t.Fatal("ISE report compile failed", err)
			}
			drift, err := plans[0].observe(context.Background())
			if mode == "wrong-status-identity" || mode == "failed" {
				if err == nil || strings.Contains(err.Error(), "private-value") {
					t.Fatal("unsafe integration status accepted or exposed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			expected := 1
			if mode == "match" {
				expected = 0
			}
			if mode == "missing" {
				expected = 2
				if calls != 0 {
					t.Fatal("integration queried before registration")
				}
			}
			if len(drift) != expected {
				t.Fatal("wrong ISE drift count", len(drift))
			}
			for _, entry := range drift {
				if entry.Path != "/authentication_and_policy_servers/ise" {
					t.Fatal("wrong upstream model path")
				}
			}
			raw, _ := json.Marshal(drift)
			if strings.Contains(string(raw), "private-value") {
				t.Fatal("report leaked credentials")
			}
		})
	}
}
func TestISEReportRejectsCredentialIntent(t *testing.T) {
	for _, extra := range []string{`"password":"private-value",`, `"password":null,`, `"password_version":1,`, `"shared_secret_version":1,`} {
		raw := strings.Replace(iseReportModel(), `"ise":{`, `"ise":{`+extra, 1)
		if _, err := parseSystemSettingsReport([]byte(raw)); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("ISE report accepted credential intent")
		}
	}
}
