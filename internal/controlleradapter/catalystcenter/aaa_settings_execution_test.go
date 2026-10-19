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

func aaaApplyModel() []byte {
	return aaaModel(strings.ReplaceAll(aaaProfile, `"server_type":`, `"shared_secret_version":1,"server_type":`))
}

func TestAAASettingsApplyParser(t *testing.T) {
	raw := aaaApplyModel()
	bindings, err := parseAAASettingsApply(raw)
	if err != nil || len(bindings) != 1 || len(bindings[0].settings[0].aaaCredentials) != 2 {
		t.Fatal("valid model rejected", err)
	}
	fallback := strings.Replace(string(raw), `,"pan":"192.0.2.30"`, "", 1)
	b, err := parseAAASettingsApply([]byte(fallback))
	if err != nil || b[0].settings[0].aaa["aaaClient"]["pan"] != "192.0.2.20" {
		t.Fatal("ISE fallback failed", err)
	}
	for _, replacement := range []string{`"shared_secret_version":0`, `"shared_secret_version":1.5`, `"shared_secret_version":"1"`, `"shared_secret_version":null`, `"shared_secret_version":1,"shared_secret":"DO-NOT-PRINT"`, `"shared_secret_version":1,"shared_secret":""`, `"unknown":1`} {
		bad := strings.Replace(string(raw), `"shared_secret_version":1`, replacement, 1)
		if _, err := parseAAASettingsApply([]byte(bad)); err == nil || strings.Contains(err.Error(), "DO-NOT-PRINT") {
			t.Fatal("invalid secret accepted or leaked")
		}
	}
	if _, err := parseNetworkSettings(raw); err == nil {
		t.Fatal("reporting must not claim write-only verification")
	}
	for _, path := range []string{"/aaa_servers/01/network_aaa/shared_secret", "/aaa_servers/-1/network_aaa/shared_secret", "/aaa_servers/0/other/shared_secret", "/aaa_servers/0/network_aaa/password"} {
		if aaaCredentialKind(path) != "" {
			t.Fatal("invalid credential path accepted")
		}
	}
}

func aaaExecutorFixture(t *testing.T) *aaaSettingsExecutor {
	t.Helper()
	bindings, err := parseAAASettingsApply(aaaApplyModel())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &configurationSecretSnapshot{uid: "owner"}
	for _, leaf := range bindings[0].settings[0].aaaCredentials {
		snapshot.values = append(snapshot.values, configurationSecretValue{
			ref:    configapi.NetworkControllerSecretRef{Section: "network_settings", Path: leaf.path, Source: leaf.field},
			source: ciskov1.NetworkControllerIntentSecretSource{Name: "aaa-secrets", Key: leaf.field},
			value:  versionedSecretFixture(t, 1, "DO-NOT-PRINT"),
		})
	}
	return &aaaSettingsExecutor{networkConfigurationExecutor: networkConfigurationExecutor{bindings: bindings}, secrets: snapshot, owner: "owner"}
}

func TestAAASettingsExecutorLifecycle(t *testing.T) {
	for _, scenario := range []string{"initial", "noop", "rotate", "metadata", "remove-secondary", "foreign", "replaced", "rollback", "remapped", "duplicate", "readback-missing", "bad-task"} {
		t.Run(scenario, func(t *testing.T) {
			e := aaaExecutorFixture(t)
			_, versions, err := resolveVersionedConfigurationCredentials(e.secrets, e.owner, "network_settings", e.bindings[0].settings[0].aaaCredentials)
			if err != nil {
				t.Fatal(err)
			}
			receipt := ciskov1.NetworkControllerConfigurationCredentialApplication{Section: "network_settings", Resource: "Global#aaa", ConfigUID: e.owner, TargetID: "root", OperationID: strings.Repeat("a", 32), Versions: versions}
			actual := aaaResponse
			switch scenario {
			case "initial", "bad-task":
				actual = `{"aaaNetwork":null,"aaaClient":{}}`
			case "metadata":
				actual = strings.Replace(actual, "192.0.2.10", "192.0.2.11", 1)
			case "remove-secondary":
				actual = strings.Replace(actual, `"serverType":"AAA"`, `"serverType":"AAA","secondaryServerIp":"192.0.2.11"`, 1)
			case "readback-missing":
				actual = `{"aaaNetwork":null}`
			case "rotate":
				for i := range e.bindings[0].settings[0].aaaCredentials {
					e.bindings[0].settings[0].aaaCredentials[i].version = 2
					e.secrets.values[i].value = versionedSecretFixture(t, 2, "DO-NOT-PRINT")
				}
			case "replaced":
				receipt.TargetID = "old-root"
			case "rollback":
				receipt.Versions[0].Version = 2
			case "remapped":
				receipt.Versions[0].SecretKey = "old-key"
			}
			if scenario != "initial" && scenario != "foreign" && scenario != "bad-task" {
				e.receipts = []ciskov1.NetworkControllerConfigurationCredentialApplication{receipt}
			}
			if scenario == "duplicate" {
				e.receipts = append(e.receipts, receipt)
			}
			writes := 0
			e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case r.URL.Path == authenticationPolicyServersPath:
					fmt.Fprint(w, aaaRegistrationFixture)
				case r.URL.Path == sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case r.URL.Path == sitesPath+"/root/aaaSettings" && r.Method == http.MethodGet:
					if r.URL.Query().Get("_inherited") != "false" {
						t.Error("inherited values must not establish ownership")
					}
					fmt.Fprint(w, `{"response":`+actual+`}`)
				case r.URL.Path == sitesPath+"/root/aaaSettings" && r.Method == http.MethodPut:
					writes++
					var body map[string]map[string]string
					if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 2 {
						t.Error("invalid replacement body")
					}
					for _, g := range aaaGroups {
						if body[g.api]["sharedSecret"] != "DO-NOT-PRINT" {
							t.Error("projected credential missing")
						}
						delete(body[g.api], "sharedSecret")
					}
					b, _ := json.Marshal(body)
					actual = string(b)
					if scenario == "bad-task" {
						fmt.Fprint(w, `{"response":{}}`)
					} else {
						fmt.Fprint(w, `{"response":{"taskId":"task"}}`)
					}
				default:
					t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
					w.WriteHeader(404)
				}
			})
			write, err := e.next(context.Background())
			invalid := scenario == "foreign" || scenario == "replaced" || scenario == "rollback" || scenario == "remapped" || scenario == "duplicate" || scenario == "readback-missing"
			if invalid {
				if err == nil || write != nil || writes != 0 {
					t.Fatal("unsafe plan accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "noop" {
				if write != nil {
					t.Fatal("verified versions replayed")
				}
				return
			}
			if write == nil || writes != 0 {
				t.Fatal("missing plan or premature mutation")
			}
			task, err := write.submit(context.Background())
			if scenario == "bad-task" {
				if err == nil {
					t.Fatal("missing task accepted")
				}
				return
			}
			if err != nil || task.ID != "task" || writes != 1 {
				t.Fatal("submission failed", err)
			}
			op := &ciskov1.NetworkControllerConfigurationOperation{Section: "network_settings", Resource: write.resource, Action: write.action, ConfigUID: e.owner, TargetID: write.identity.target, CredentialVersions: write.identity.credentials}
			id, ok, err := e.verifyCredential(context.Background(), op)
			if err != nil || !ok || id != "root" {
				t.Fatal("readback failed", err)
			}
			// A durable shared-journal receipt is required before convergence. Metadata
			// equality alone must not turn the initial write into an adopted secret.
			e.receipts = []ciskov1.NetworkControllerConfigurationCredentialApplication{{Section: op.Section, Resource: op.Resource, ConfigUID: e.owner, TargetID: id, OperationID: strings.Repeat("b", 32), Versions: op.CredentialVersions}}
			again, err := e.next(context.Background())
			if err != nil || again != nil {
				t.Fatal("receipt did not prevent replay", err)
			}
			op.TargetID = "replaced"
			if _, ok, err := e.verifyCredential(context.Background(), op); err == nil || ok {
				t.Fatal("replacement accepted")
			}
		})
	}
}
