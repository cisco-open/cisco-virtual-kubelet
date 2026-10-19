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

	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

func policyServerFixture() authenticationPolicyServer {
	no := false
	auth, acct, retries, timeout := 1812, 1813, 1, 5
	return authenticationPolicyServer{IP: "192.0.2.43", ISE: &no, Protocol: "RADIUS", Role: "primary", AuthenticationPort: &auth, AccountingPort: &acct, Retries: &retries, Timeout: &timeout}
}
func TestAuthenticationPolicyServerRegistration(t *testing.T) {
	for _, mode := range []string{"create", "exists", "missing-receipt", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			desired := policyServerFixture()
			actual := []authenticationPolicyServer{}
			writes := 0
			if mode == "exists" {
				server := desired
				server.ID = "existing"
				actual = append(actual, server)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != authenticationPolicyServersPath {
					t.Error("unexpected path")
					w.WriteHeader(404)
					return
				}
				if r.Method == "GET" {
					json.NewEncoder(w).Encode(map[string]any{"response": actual})
					return
				}
				if r.Method != "POST" {
					t.Error("unexpected method")
				}
				var payload map[string]any
				json.NewDecoder(r.Body).Decode(&payload)
				if payload["sharedSecret"] != "fixture-secret" || payload["isIseEnabled"] != false || payload["role"] != "primary" {
					t.Error("incorrect registration payload")
				}
				server := desired
				server.ID = "created"
				actual = []authenticationPolicyServer{server}
				writes++
				if mode == "missing-receipt" {
					fmt.Fprint(w, `{}`)
				} else {
					fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
				}
			})
			write, err := c.planAuthenticationPolicyServerRegistration(context.Background(), desired, "fixture-secret")
			if mode == "exists" {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("adopted existing server")
				}
				return
			}
			if err != nil || write == nil {
				t.Fatal(err)
			}
			_, err = write.submit(context.Background())
			if mode == "missing-receipt" {
				if err == nil || writes != 1 {
					t.Fatal("uncertain receipt accepted or replayed")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Resource: write.resource, Action: write.action}
			id, ok, err := c.verifyAuthenticationPolicyServerRegistration(context.Background(), desired, op)
			if err != nil || !ok || id != "created" {
				t.Fatal("registration metadata failed", err)
			}
			if mode == "replacement" {
				op.TargetID = id
				actual[0].ID = "replacement"
				if _, ok, err = c.verifyAuthenticationPolicyServerRegistration(context.Background(), desired, op); err == nil || ok {
					t.Fatal("replaced server accepted")
				}
			}
			if _, err = c.planAuthenticationPolicyServerRegistration(context.Background(), desired, "fixture-secret"); err == nil || writes != 1 {
				t.Fatal("registration repeated")
			}
		})
	}
}
func TestAuthenticationPolicyServerObservation(t *testing.T) {
	for _, body := range []string{`{"response":[{"id":"wrong","ipAddress":"192.0.2.43","protocol":"RADIUS"}]}`, `{"response":null}`, `{"response":[{"instanceUuid":"a","ipAddress":"192.0.2.43","protocol":"RADIUS"},{"instanceUuid":"b","ipAddress":"192.0.2.43","protocol":"RADIUS"}]}`} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			fmt.Fprint(w, body)
		})
		if _, err := c.authenticationPolicyServers(context.Background()); err == nil {
			t.Fatal("invalid identity projection accepted")
		}
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		fmt.Fprint(w, `{"response":[{"instanceUuid":"server","ipAddress":"192.0.2.43","protocol":"RADIUS","sharedSecret":"must-not-retain","messageKey":"must-not-retain"}]}`)
	})
	servers, err := c.authenticationPolicyServers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(servers)
	if strings.Contains(string(raw), "must-not-retain") || strings.Contains(string(raw), "sharedSecret") {
		t.Fatal("credential retained in metadata projection")
	}
	if present, err := c.hasAuthenticationPolicyServer(context.Background()); err != nil || !present {
		t.Fatal("instanceUuid server not recognized", err)
	}
}
