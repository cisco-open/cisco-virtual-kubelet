// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
)

func TestGlobalCredentialMetadataMixedWithVersionedCredentials(t *testing.T) {
	ctx := context.Background()
	a, config := noAuthCredentialConfigurationFixture(t)
	want, snapshot := globalCredentialFixture(t, "snmpv2_read_credentials")
	leaf := want.credentials[0]
	config.Spec.SecretRefs = []configapi.NetworkControllerSecretRef{{Section: "network_settings", Path: leaf.path, Source: "snmp-read"}}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.IntentSecretSources = []cvk.NetworkControllerIntentSecretSource{{Alias: "snmp-read", Name: "lab", Key: "read"}}
	if err := a.statusWriter.Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	a.intentSecretPath = root
	relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "network_settings", JSONPointer: leaf.path, SourceAlias: "snmp-read", SecretName: "lab", SecretKey: "read"})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, relative)
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, snapshot.values[0].value, 0600); err != nil {
		t.Fatal(err)
	}
	var model map[string]any
	_ = json.Unmarshal(config.Spec.Source.Inline.Raw, &model)
	groups := model["catalyst_center"].(map[string]any)["network_settings"].(map[string]any)["device_credentials"].(map[string]any)
	var entries []any
	_ = json.Unmarshal([]byte("["+globalCredentialFixtureEntries[want.kind.model]+"]"), &entries)
	groups[want.kind.model] = entries
	config.Spec.Source.Inline.Raw, _ = json.Marshal(model)
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	actual := map[string][]globalCredentialMetadata{}
	writes := []string{}
	tasks := map[string]int64{}
	expectedSecret := "private-" + leaf.field
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == globalCredentialsPath && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"response": actual})
		case r.URL.Path == globalCredentialsPath && (r.Method == http.MethodPost || r.Method == http.MethodPut):
			var body map[string]json.RawMessage
			_ = json.NewDecoder(r.Body).Decode(&body)
			if len(body) != 1 {
				t.Error("invalid mixed payload")
			}
			for kind, raw := range body {
				if kind != "snmpV3" && kind != "snmpV2cRead" {
					t.Error("unexpected credential kind")
				}
				var payload map[string]any
				if r.Method == http.MethodPost {
					var list []map[string]any
					_ = json.Unmarshal(raw, &list)
					if len(list) != 1 || len(actual[kind]) != 0 {
						t.Error("duplicate mixed create")
						w.WriteHeader(400)
						return
					}
					payload = list[0]
				} else {
					_ = json.Unmarshal(raw, &payload)
					if payload["id"] != "owned-"+kind {
						t.Error("mixed update used wrong UUID")
					}
				}
				if kind == "snmpV2cRead" && payload[leaf.field] != expectedSecret || kind == "snmpV3" && (payload["authPassword"] != nil || payload["privacyPassword"] != nil || payload["snmpMode"] != "NOAUTHNOPRIV") {
					t.Error("mixed credential payload violated secret binding")
				}
				encoded, _ := json.Marshal(payload)
				var got globalCredentialMetadata
				_ = json.Unmarshal(encoded, &got)
				got.ID = "owned-" + kind
				actual[kind] = []globalCredentialMetadata{got}
				writes = append(writes, kind)
				id := fmt.Sprintf("mixed-task-%d", len(writes))
				tasks[id] = time.Now().UnixMilli()
				fmt.Fprintf(w, `{"response":{"taskId":%q}}`, id)
			}
		case strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			row := map[string]any{"id": id, "startTime": tasks[id], "endTime": tasks[id], "isError": false}
			var response any = row
			if strings.HasSuffix(r.URL.Path, "/tree") {
				response = []any{row}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": response})
		default:
			t.Errorf("unexpected mixed credential request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	noAuthCredentialConverge(t, a, config)
	state := journalState(t, a)
	if !reflect.DeepEqual(writes, []string{"snmpV2cRead", "snmpV3"}) || len(state.ActionApplications) != 1 || len(state.CredentialApplications) != 1 {
		t.Fatal("mixed receipts did not converge independently", writes)
	}
	retained := state.ActionApplications[0]
	config.Generation++
	config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(config.Spec.Source.Inline.Raw), `"read_community_version":1`, `"read_community_version":2`, 1))
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		noAuthCredentialTick(t, a, config)
	}
	if len(writes) != 2 || journalState(t, a).Operation != nil {
		t.Fatal("projection lag allowed mixed submission")
	}
	expectedSecret = "rotated-private-community"
	if err := os.WriteFile(path, versionedSecretFixture(t, 2, expectedSecret), 0600); err != nil {
		t.Fatal(err)
	}
	noAuthCredentialConverge(t, a, config)
	state = journalState(t, a)
	if !reflect.DeepEqual(writes, []string{"snmpV2cRead", "snmpV3", "snmpV2cRead"}) || !reflect.DeepEqual(retained, state.ActionApplications[0]) || state.CredentialApplications[0].Versions[0].Version != 2 {
		t.Fatal("mixed rotation replayed or corrupted metadata ownership", writes)
	}
}

func TestGlobalCredentialMetadataCombinedNetworkPreflight(t *testing.T) {
	for _, missingSite := range []bool{false, true} {
		t.Run(fmt.Sprintf("missing-site=%t", missingSite), func(t *testing.T) {
			a, config := noAuthCredentialConfigurationFixture(t)
			var model map[string]any
			_ = json.Unmarshal(config.Spec.Source.Inline.Raw, &model)
			model["catalyst_center"].(map[string]any)["sites"] = map[string]any{"areas": []any{map[string]any{"name": "Lab", "parent_name": "Global", "snmpv3_credentials": "fixture"}}}
			raw, _ := json.Marshal(model)
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet {
					t.Error("combined preflight performed a mutation")
				}
				switch r.URL.Path {
				case globalCredentialsPath:
					fmt.Fprint(w, `{"response":{}}`)
				case sitesPath:
					sites := sampleSites()
					if missingSite {
						sites = sites[:1]
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": sites})
				case siteCredentialsPath("area"):
					slots := map[string]any{}
					for _, kind := range globalCredentialKinds {
						slots[credentialSlot(kind.model)] = nil
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": slots})
				default:
					t.Errorf("unexpected combined preflight endpoint %s", r.URL.Path)
					http.NotFound(w, r)
				}
			})
			nc, err := a.currentController(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			plan, err := a.compileNetworkSettingsApply(nc, config, &configSourceSnapshot{raw: raw})
			if err != nil {
				t.Fatal(err)
			}
			write, err := plan.executor.next(context.Background())
			if missingSite {
				if err == nil || write != nil {
					t.Fatal("credential write escaped missing downstream site")
				}
			} else if err != nil || write == nil || !isMetadataCredentialOperation("network_settings", write.resource) || write.identity.target != "" || len(write.identity.credentials) != 0 {
				t.Fatal("metadata definition was not planned before its site assignment", err)
			}
		})
	}
}
