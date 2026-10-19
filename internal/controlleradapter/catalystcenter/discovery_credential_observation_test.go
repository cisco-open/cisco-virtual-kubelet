// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestDiscoveryCredentialCreateObservation(t *testing.T) {
	for _, mode := range []string{"complete", "mixed", "pending-task", "failed-task", "wrong-task", "future-task", "missing-result", "wrong-job", "missing-job", "duplicate-job", "pending-job", "wrong-user", "duplicate-user", "version-mismatch", "wrong-input", "wrong-action", "unknown-phase", "foreign-owner", "journal-changed", "source-changed", "projection-changed", "global-replaced", "global-missing", "native-error"} {
		t.Run(mode, func(t *testing.T) {
			discoveryCredentialCreateObservationScenario(t, mode, nil)
		})
	}
}

func discoveryCredentialCreateObservationScenario(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	ctx := context.Background()
	mixed := mode == "mixed" || strings.HasPrefix(mode, "global-")
	raw := discoveryCredentialSource(t, map[string]any{"user_name_list": []any{"operator", "admin"}, "password_list_version": 1}, mixed)
	a, nc, config, source, paths := discoveryProjectionFixture(t, raw, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`})
	if install != nil {
		install(a, config)
		if err := a.statusReader.Get(ctx, ctrlclient.ObjectKeyFromObject(config), config); err != nil {
			t.Fatal(err)
		}
		var err error
		nc, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
	}

	p, err := a.prepareDiscoveryCredentialProjection(nc, config, source)
	if err != nil {
		t.Fatal(err)
	}
	globals := map[string]map[string]globalCredentialMetadata{"cli": {"lab-cli": {ID: "global-id"}}}
	request, input, err := p.bindRequest(nc, config, source, 0, globals)
	if err != nil {
		t.Fatal(err)
	}
	stamp := metav1.NewTime(time.Now().Add(-time.Second).Truncate(time.Second))
	op := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: config.Name, ConfigUID: string(config.UID), ConfigGeneration: config.Generation, ControllerGeneration: nc.Generation, Section: "inventory", Resource: input, Action: "Create", Phase: "Verifying", TaskID: "original-task", SubmittedAt: &stamp, CredentialVersions: request.versions}
	switch mode {
	case "version-mismatch":
		op.CredentialVersions[0].Version++
	case "wrong-input":
		op.Resource += "changed"
	case "wrong-action":
		op.Action = "Update"
	case "unknown-phase":
		op.Phase = "Unknown"
	}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: op.DeepCopy(), Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigUID: string(config.UID), ConfigName: config.Name}}}
	if mode == "foreign-owner" {
		nc.Status.Configuration.Owners[0].ConfigUID = "other"
	}
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	before := nc.Status.Configuration.DeepCopy()
	taskReads, inventoryReads, globalReads, writes := 0, 0, 0, 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"fixture"}`)
			return
		}
		if r.Method != "GET" {
			writes++
			w.WriteHeader(500)
			return
		}
		switch r.URL.Path {
		case globalCredentialsPath:
			globalReads++
			id := "global-id"
			if mode == "global-replaced" && globalReads > 1 {
				id = "replacement"
			}
			rows := []map[string]any{{"id": id, "description": "lab-cli"}}
			if mode == "global-missing" {
				rows = nil
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"cliCredential": rows}})
		case taskPath + "original-task":
			taskReads++
			data := map[string]any{"id": "original-task", "startTime": stamp.UnixMilli(), "endTime": stamp.UnixMilli(), "isError": false, "data": `{"discoveryId":"job-id"}`}
			switch mode {
			case "pending-task":
				delete(data, "endTime")
			case "failed-task":
				data["isError"] = true
			case "wrong-task":
				data["id"] = "other-task"
			case "future-task":
				data["startTime"] = time.Now().Add(time.Hour).UnixMilli()
			case "missing-result":
				delete(data, "data")
			}
			json.NewEncoder(w).Encode(map[string]any{"response": data})
		case discoveryPath + "/1/100":
			inventoryReads++
			got := discoveryMetadata{ID: "job-id", Name: "fixture", Type: "RANGE", Addresses: "192.0.2.1-192.0.2.1", Protocol: "SSH", Condition: "Complete", Status: "Inactive", Usernames: []string{"operator", "admin"}}
			if mixed {
				got.CredentialIDs = []string{"global-id"}
			}
			rows := []discoveryMetadata{got}
			switch mode {
			case "wrong-job":
				rows[0].ID = "other-job"
			case "missing-job":
				rows = []discoveryMetadata{}
			case "duplicate-job":
				rows = append(rows, got)
			case "pending-job":
				rows[0].Condition = "In Progress"
			case "wrong-user":
				rows[0].Usernames = []string{"different"}
			case "duplicate-user":
				rows[0].Usernames = []string{"admin", "admin"}
			case "journal-changed":
				fresh, _ := a.currentController(ctx)
				fresh.Status.Configuration.Operation.TaskID = "replacement-task"
				if err := a.statusWriter.Status().Update(ctx, fresh); err != nil {
					t.Error(err)
				}
			case "source-changed":
				config.Spec.Source.Inline.Raw = []byte(`{}`)
				if err := a.statusWriter.Update(ctx, config.DeepCopy()); err != nil {
					t.Error(err)
				}
			case "projection-changed":
				if err := os.WriteFile(paths["passwordList"], versionedSecretFixture(t, 1, `["changed"]`), 0600); err != nil {
					t.Error(err)
				}
			case "native-error":
				w.WriteHeader(500)
				fmt.Fprint(w, discoveryCredentialMarker)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"response": rows})
		default:
			t.Error("unexpected API path", r.URL.Path)
			w.WriteHeader(500)
		}
	})
	observation, err := a.observeDiscoveryCredentialCreate(ctx, config, source, p, 0, op)
	ok := mode == "complete" || mode == "mixed"
	if (err == nil) != ok || (observation != nil) != ok || writes != 0 {
		t.Fatal("unexpected read-only qualification", err, writes, "task/inventory reads", taskReads, inventoryReads)
	}
	if err != nil && strings.Contains(err.Error(), discoveryCredentialMarker) {
		t.Fatal("native error leaked credential marker")
	}
	if ok && (observation.id != "job-id" || observation.input != input || observation.operationID != op.ID || observation.controllerVersion == "" || taskReads != 1 || inventoryReads != 1) {
		t.Fatal("task/readback binding incomplete")
	}
	fresh, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if mode != "journal-changed" && !reflect.DeepEqual(before, fresh.Status.Configuration) {
		t.Fatal("observer wrote receipt or advanced journal")
	}
}

func TestDiscoveryCredentialPublicReadback(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields map[string]any
		values map[string]string
	}{
		{"cli", map[string]any{"user_name_list": []any{"operator"}, "password_list_version": 1}, map[string]string{"passwordList": `["CVK_SENSITIVE_TEST_VALUE"]`}},
		{"http", map[string]any{"http_read_credential_version": 1, "http_write_credential_version": 1}, map[string]string{"httpReadCredential": discoveryCredentialMarker, "httpWriteCredential": discoveryCredentialMarker}},
		{"snmp-v2", map[string]any{"snmp_version": "v2", "snmp_ro_community_version": 1, "snmp_rw_community_version": 1, "snmp_ro_community_desc": "read", "snmp_rw_community_desc": "write"}, map[string]string{"snmpRoCommunity": discoveryCredentialMarker, "snmpRwCommunity": discoveryCredentialMarker}},
		{"snmp-v3", map[string]any{"snmp_version": "v3", "snmp_user_name": "operator", "snmp_mode": "AuthPriv", "snmp_auth_protocol": "SHA", "snmp_priv_protocol": "AES128", "snmp_auth_passphrase_version": 1, "snmp_priv_passphrase_version": 1}, map[string]string{"snmpAuthPassphrase": discoveryCredentialMarker, "snmpPrivPassphrase": discoveryCredentialMarker}},
	} {
		t.Run(test.name, func(t *testing.T) {
			a, nc, c, source, _ := discoveryProjectionFixture(t, discoveryCredentialSource(t, test.fields, false), test.values)
			p, err := a.prepareDiscoveryCredentialProjection(nc, c, source)
			if err != nil {
				t.Fatal(err)
			}
			r, _, err := p.bindRequest(nc, c, source, 0, nil)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := json.Marshal(r.public)
			if strings.Contains(string(body), discoveryCredentialMarker) {
				t.Fatal("public effect contains secret material")
			}
			var native map[string]any
			if json.Unmarshal(body, &native) != nil {
				t.Fatal("invalid public effect")
			}
			// Even a controller that echoes or masks every credential leaf must
			// not cause those values to enter discovery metadata or comparison.
			for _, field := range discoveryCredentialFields {
				native[field.api] = discoveryCredentialMarker
			}
			encoded, _ := json.Marshal(native)
			var got discoveryMetadata
			if json.Unmarshal(encoded, &got) != nil {
				t.Fatal("invalid readback")
			}
			encoded, _ = json.Marshal(got)
			if strings.Contains(string(encoded), discoveryCredentialMarker) {
				t.Fatal("readback retained secret material")
			}
			matches, err := discoveryMatches(r.public, got)
			if err != nil || !matches {
				t.Fatal("allowlisted readback did not match", err)
			}
			if _, err := checkedDiscoveryCredentialEffect(r.public, got); err != nil {
				t.Fatal("credential echoes affected public effect verification", err)
			}
			for key := range p.intents[0].metadata {
				t.Run(key, func(t *testing.T) {
					changed := map[string]any{}
					json.Unmarshal(body, &changed)
					if key == "userNameList" {
						changed[key] = []string{"different"}
					} else {
						changed[key] = "different"
					}
					encoded, _ := json.Marshal(changed)
					var got discoveryMetadata
					if json.Unmarshal(encoded, &got) != nil {
						t.Fatal("invalid changed fixture")
					}
					if matches, _ := discoveryMatches(r.public, got); matches {
						t.Fatal("changed public metadata accepted")
					}
					if _, err := checkedDiscoveryCredentialEffect(r.public, got); err == nil {
						t.Fatal("changed public effect accepted")
					}
				})
			}
		})
	}
}
