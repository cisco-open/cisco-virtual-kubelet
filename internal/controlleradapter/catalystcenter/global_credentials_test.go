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

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
)

var globalCredentialFixtureEntries = map[string]string{
	"cli_credentials":          `{"name":"fixture","username":"tester","password_version":1,"enable_version":1}`,
	"https_read_credentials":   `{"name":"fixture","username":"tester","port":443,"password_version":1}`,
	"https_write_credentials":  `{"name":"fixture","username":"tester","port":443,"password_version":1}`,
	"snmpv2_read_credentials":  `{"name":"fixture","read_community_version":1}`,
	"snmpv2_write_credentials": `{"name":"fixture","write_community_version":1}`,
	"snmpv3_credentials":       `{"name":"fixture","username":"tester","snmp_mode":"AUTHPRIV","auth_type":"SHA","privacy_type":"AES128","auth_password_version":1,"privacy_password_version":1}`,
}

func globalCredentialModel(kind, entry string) []byte {
	return []byte(`{"catalyst_center":{"network_settings":{"device_credentials":{"` + kind + `":[` + entry + `]}}}}`)
}
func globalCredentialFixture(t *testing.T, kind string) (globalCredentialIntent, *configurationSecretSnapshot) {
	t.Helper()
	desired, err := parseGlobalCredentials(globalCredentialModel(kind, globalCredentialFixtureEntries[kind]), true)
	if err != nil || len(desired) != 1 {
		t.Fatal("global credential model failed", err)
	}
	snapshot := &configurationSecretSnapshot{uid: "owner"}
	for _, leaf := range desired[0].credentials {
		snapshot.values = append(snapshot.values, configurationSecretValue{ref: configapi.NetworkControllerSecretRef{Section: "network_settings", Path: leaf.path, Source: leaf.field}, source: ciskov1.NetworkControllerIntentSecretSource{Alias: leaf.field, Name: "fixture", Key: leaf.field}, value: versionedSecretFixture(t, 1, "private-"+leaf.field)})
	}
	return desired[0], snapshot
}
func TestGlobalCredentialModelPayloadAndCreate(t *testing.T) {
	for _, kind := range globalCredentialKinds {
		t.Run(kind.model, func(t *testing.T) {
			want, snapshot := globalCredentialFixture(t, kind.model)
			var actual *globalCredentialMetadata
			writes := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != globalCredentialsPath {
					t.Error("unexpected credential endpoint")
				}
				if r.Method == "GET" {
					list := []globalCredentialMetadata{}
					if actual != nil {
						list = append(list, *actual)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{kind.api: list}})
					return
				}
				writes++
				if r.Method != "POST" {
					t.Error("unexpected credential method")
				}
				var body map[string][]map[string]any
				if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 || len(body[kind.api]) != 1 {
					t.Fatal("invalid create payload")
				}
				payload := body[kind.api][0]
				for _, leaf := range want.credentials {
					if payload[leaf.field] != "private-"+leaf.field {
						t.Error("missing projected field")
					}
				}
				if payload["id"] != nil || payload["credentialType"] != nil {
					t.Error("read identity in create payload")
				}
				data, _ := json.Marshal(payload)
				actual = &globalCredentialMetadata{}
				_ = json.Unmarshal(data, actual)
				actual.ID = "credential-id"
				fmt.Fprint(w, `{"response":{"taskId":"credential-task"}}`)
			})
			write, err := c.planGlobalCredentialCreate(context.Background(), want, snapshot, "owner")
			if err != nil || write == nil || len(write.identity.credentials) != len(want.credentials) {
				t.Fatal("create plan failed", err)
			}
			if task, err := write.submit(context.Background()); err != nil || task.ID != "credential-task" {
				t.Fatal("invalid task receipt", err)
			}
			observed, err := c.globalCredentials(context.Background())
			if err != nil || !globalCredentialMatches(want.metadata, observed[kind.model]["fixture"]) {
				t.Fatal("metadata readback failed", err)
			}
			raw, _ := json.Marshal(observed)
			if strings.Contains(string(raw), "private-") {
				t.Fatal("read model retained credential material")
			}
			if _, err := c.planGlobalCredentialCreate(context.Background(), want, snapshot, "owner"); err == nil || writes != 1 {
				t.Fatal("existing credential adopted or rewritten")
			}
			raw, versions, err := want.payload(snapshot, "owner", "credential-id")
			var update map[string]map[string]any
			if err != nil || json.Unmarshal(raw, &update) != nil || update[kind.api]["id"] != "credential-id" || len(versions) != len(want.credentials) {
				t.Fatal("update payload identity/shape failed", err)
			}
			for _, leaf := range want.credentials {
				if update[kind.api][leaf.field] != "private-"+leaf.field {
					t.Fatal("full update omitted a credential")
				}
			}
			want.credentials[0].version = 2
			if _, _, err := want.payload(snapshot, "owner", ""); err == nil {
				t.Fatal("stale credential projection used")
			}
		})
	}
}
func TestGlobalCredentialParserRejectsUnsafeIntent(t *testing.T) {
	for _, entry := range []string{
		`{"name":"fixture","username":"tester","password":"private-value","password_version":1}`,
		`{"name":"fixture","username":"tester","password_version":0}`,
		`{"name":"fixture","username":"tester"}`,
		`{"name":"fixture","username":"tester","password_version":1,"enable":"private-value"}`,
		`{"name":"fixture","username":"tester","password_version":1,"unknown":true}`,
	} {
		if _, err := parseGlobalCredentials(globalCredentialModel("cli_credentials", entry), true); err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("unsafe credential model accepted or exposed")
		}
	}
	for _, entry := range []string{
		`{"name":"fixture","username":"tester","snmp_mode":"AUTHPRIV","auth_type":"SHA","auth_password_version":1}`,
		`{"name":"fixture","username":"tester","snmp_mode":"AUTHNOPRIV","auth_type":"SHA","auth_password_version":1,"privacy_password_version":1}`,
		`{"name":"fixture","username":"tester","snmp_mode":"NOAUTHNOPRIV","auth_type":"SHA"}`,
	} {
		if _, err := parseGlobalCredentials(globalCredentialModel("snmpv3_credentials", entry), true); err == nil {
			t.Fatal("contradictory SNMP security mode accepted")
		}
	}
	for _, mode := range []string{"AUTHNOPRIV", "NOAUTHNOPRIV"} {
		entry := `{"name":"fixture","username":"tester","snmp_mode":"` + mode + `"`
		if mode == "AUTHNOPRIV" {
			entry += `,"auth_type":"SHA","auth_password_version":1`
		}
		if _, err := parseGlobalCredentials(globalCredentialModel("snmpv3_credentials", entry+`}`), true); err != nil {
			t.Fatal("valid SNMP mode rejected", err)
		}
	}
}
func TestGlobalCredentialReadRejectsAmbiguousIdentity(t *testing.T) {
	for _, body := range []string{
		`{"response":null}`,
		`{"response":{"cliCredential":[{"id":"a","description":"same"},{"id":"b","description":"same"}]}}`,
		`{"response":{"cliCredential":[{"id":"a","description":"one"}],"httpsRead":[{"id":"a","description":"two"}]}}`,
		`{"response":{"cliCredential":[{"description":"missing-id"}]}}`,
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == authPath {
				fmt.Fprint(w, `{"Token":"test"}`)
				return
			}
			fmt.Fprint(w, body)
		})
		if _, err := c.globalCredentials(context.Background()); err == nil {
			t.Fatal("ambiguous global credential inventory accepted")
		}
	}
}
