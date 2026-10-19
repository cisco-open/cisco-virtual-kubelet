// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func TestGlobalCredentialManifestCreateRotateRestart(t *testing.T) {
	exerciseGlobalCredentialManifest(t, false)
}
func TestGlobalCredentialManifestKnownTaskRecovery(t *testing.T) {
	exerciseGlobalCredentialManifest(t, true)
}
func exerciseGlobalCredentialManifest(t *testing.T, recoverTasks bool, installers ...func(*testing.T, *adapter, *configapi.NetworkControllerConfig)) {
	for _, kind := range globalCredentialKinds {
		for _, combined := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/combined=%t", kind.model, combined), func(t *testing.T) {
				ctx := context.Background()
				a, config, source, _ := credentialVersionJournalFixture(t)
				nc, err := a.currentController(ctx)
				if err != nil {
					t.Fatal(err)
				}
				nc.Spec.Endpoint = "https://catc.example"
				nc.Spec.CredentialSecretRef = ciskov1.NetworkControllerSecretReference{Name: "credentials"}
				nc.Spec.IntentSecretSources = nil
				config.Spec.SecretRefs = nil
				config.Spec.ManagedSections = []string{"network_settings"}
				want, snapshot := globalCredentialFixture(t, kind.model)
				paths := map[string]string{}
				for _, value := range snapshot.values {
					alias := strings.ToLower(value.ref.Source)
					nc.Spec.IntentSecretSources = append(nc.Spec.IntentSecretSources, ciskov1.NetworkControllerIntentSecretSource{Alias: alias, Name: "credentials", Key: alias})
					config.Spec.SecretRefs = append(config.Spec.SecretRefs, configapi.NetworkControllerSecretRef{Section: "network_settings", Path: value.ref.Path, Source: alias})
					relative, err := controlleradapter.IntentSecretRelativePath(controlleradapter.IntentSecretPathInput{ConfigName: config.Name, Section: "network_settings", JSONPointer: value.ref.Path, SourceAlias: alias, SecretName: "credentials", SecretKey: alias})
					if err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(source.secrets.root, relative)
					if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path, value.value, 0600); err != nil {
						t.Fatal(err)
					}
					paths[value.ref.Path] = path
				}
				if err := a.statusWriter.Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
				model := globalCredentialModel(kind.model, globalCredentialFixtureEntries[kind.model])
				if combined {
					var root map[string]any
					if json.Unmarshal(model, &root) != nil {
						t.Fatal("invalid fixture")
					}
					cc := root["catalyst_center"].(map[string]any)
					cc["sites"] = map[string]any{"areas": []any{map[string]any{"name": "Lab", "parent_name": "Global", kind.model: want.metadata.Name, "network_settings": map[string]any{"network": "lab"}}}}
					cc["network_settings"].(map[string]any)["network"] = []any{map[string]any{"name": "lab", "ntp_servers": []string{"192.0.2.1"}}}
					model, _ = json.Marshal(root)
				}
				config.Spec.ModelSource = configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true}
				config.Spec.Source.Inline = &runtime.RawExtension{Raw: model}
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				a.intentSecretPath = source.secrets.root
				if len(installers) == 1 {
					installers[0](t, a, config)
				}
				var actual *globalCredentialMetadata
				assignmentWrites, settingWrites := 0, 0
				slots := map[string]any{}
				for _, k := range globalCredentialKinds {
					slots[credentialSlot(k.model)] = nil
				}
				settings := map[string]any{"ntp": nil}
				writes := 0
				rotation := false
				a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
					switch {
					case r.URL.Path == authPath:
						fmt.Fprint(w, `{"Token":"test"}`)
					case combined && r.URL.Path == sitesPath:
						json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
					case combined && r.URL.Path == sitesPath+"/root/ntpSettings" && r.Method == "GET":
						fmt.Fprint(w, `{"response":{"ntp":null}}`)
					case combined && (r.URL.Path == siteCredentialsPath("area") || r.URL.Path == sitesPath+"/area/ntpSettings"):
						response := slots
						assignment := r.URL.Path == siteCredentialsPath("area")
						if !assignment {
							response = settings
						}
						if r.Method == "PUT" {
							receipts := journalState(t, a).CredentialApplications
							if len(receipts) != 1 || actual == nil || receipts[0].TargetID != actual.ID {
								t.Error("downstream write before verified credential receipt")
							}
							var body map[string]any
							if json.NewDecoder(r.Body).Decode(&body) != nil {
								t.Error("bad downstream body")
							}
							for k, v := range body {
								response[k] = v
							}
							if assignment {
								assignmentWrites++
								slot := body[credentialSlot(kind.model)].(map[string]any)
								if slot["credentialsId"] != "credential-id" {
									t.Error("unresolved assignment UUID")
								}
							} else {
								settingWrites++
							}
							fmt.Fprintf(w, `{"response":{"taskId":"downstream-%d-%d"}}`, assignmentWrites, settingWrites)
						} else {
							if r.URL.Query().Get("_inherited") != "false" {
								t.Error("effective read used")
							}
							json.NewEncoder(w).Encode(map[string]any{"response": response})
						}
					case strings.HasPrefix(r.URL.Path, taskPath):
						now := time.Now().UnixMilli()
						id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
						task := map[string]any{"id": id, "rootId": id, "startTime": now, "endTime": now, "isError": false}
						var response any = task
						if strings.HasSuffix(r.URL.Path, "/tree") {
							response = []any{task}
						}
						json.NewEncoder(w).Encode(map[string]any{"response": response})
					case r.URL.Path == globalCredentialsPath && r.Method == "GET":
						list := []globalCredentialMetadata{}
						if actual != nil {
							list = append(list, *actual)
						}
						_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{kind.api: list}})
					case r.URL.Path == globalCredentialsPath && (r.Method == "POST" || r.Method == "PUT"):
						var body map[string]any
						_ = json.NewDecoder(r.Body).Decode(&body)
						var payload map[string]any
						if r.Method == "POST" {
							payload = body[kind.api].([]any)[0].(map[string]any)
							if writes != 0 {
								t.Error("create repeated")
							}
						} else {
							payload = body[kind.api].(map[string]any)
							if payload["id"] != "credential-id" {
								t.Error("update target UUID missing")
							}
						}
						for i, leaf := range want.credentials {
							expected := "private-" + leaf.field
							if rotation && i == 0 {
								expected = "rotated-value"
							}
							if payload[leaf.field] != expected {
								t.Error("incorrect projected credential")
							}
						}
						raw, _ := json.Marshal(payload)
						actual = &globalCredentialMetadata{}
						_ = json.Unmarshal(raw, actual)
						actual.ID = "credential-id"
						writes++
						fmt.Fprintf(w, `{"response":{"taskId":"global-task-%d"}}`, writes)
					default:
						t.Errorf("unexpected global credential request %s %s", r.Method, r.URL.Path)
						w.WriteHeader(404)
					}
				})
				key := ctrlclient.ObjectKeyFromObject(config)
				recovered := map[string]bool{}
				tick := func() {
					t.Helper()
					restarted := *a
					if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
						t.Fatal(err)
					}
					if err := a.statusReader.Get(ctx, key, config); err != nil {
						t.Fatal(err)
					}
					if recoverTasks {
						state := journalState(t, a)
						if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" && isGlobalCredentialResource(state.Operation.Section, state.Operation.Resource) && !recovered[state.Operation.ID] {
							if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
								t.Fatal(err)
							}
							recovered[state.Operation.ID] = true
						}
					}

				}
				converge := func() {
					t.Helper()
					for i := 0; i < 24; i++ {
						tick()
						if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
							return
						}
					}
					t.Fatal("global credential manifest did not converge", config.Status.Conditions)
				}
				converge()
				if writes != 1 || len(journalState(t, a).CredentialApplications) != 1 {
					t.Fatal("credential create receipt missing")
				}
				leaf := want.credentials[0]
				parts := strings.Split(leaf.path, "/")
				counter := parts[len(parts)-1] + "_version"
				config.Generation++
				config.Spec.Source.Inline.Raw = []byte(strings.Replace(string(model), `"`+counter+`":1`, `"`+counter+`":2`, 1))
				if err := a.statusWriter.Update(ctx, config); err != nil {
					t.Fatal(err)
				}
				for i := 0; i < 3; i++ {
					tick()
				}
				if writes != 1 || journalState(t, a).Operation != nil {
					t.Fatal("stale projection submitted")
				}
				rotation = true
				if err := os.WriteFile(paths[leaf.path], versionedSecretFixture(t, 2, "rotated-value"), 0600); err != nil {
					t.Fatal(err)
				}
				converge()
				for i := 0; i < 3; i++ {
					tick()
				}
				state := journalState(t, a)
				if writes != 2 || state.Operation != nil || len(state.CredentialApplications) != 1 || state.CredentialApplications[0].TargetID != "credential-id" {
					t.Fatal("credential rotation not idempotent")
				}
				for _, v := range state.CredentialApplications[0].Versions {
					expected := int64(1)
					if v.Path == leaf.path {
						expected = 2
					}
					if v.Version != expected {
						t.Fatal("wrong applied credential counter")
					}
				}
				if recoverTasks && len(recovered) != 2 {
					t.Fatal("both acknowledged credential tasks were not recovered", len(recovered))
				}
				if combined && (assignmentWrites != 1 || settingWrites != 1) {
					t.Fatalf("downstream replay: assignments=%d settings=%d", assignmentWrites, settingWrites)
				}
				raw, _ := json.Marshal(state)
				if strings.Contains(string(raw), "private-") || strings.Contains(string(raw), "rotated-value") {
					t.Fatal("credential material persisted")
				}
			})
		}
	}
}

func TestGlobalCredentialPrunedBindingCannotReleaseJournal(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"network_settings"}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	op, err := a.prepareConfiguration(ctx, config, nil, "network_settings", "device_credentials/cli_credentials/fixture", "Create")
	if err != nil {
		t.Fatal(err)
	}
	nc, err := a.currentController(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a schema that strips both version bindings and application receipts.
	nc.Status.Configuration.Operation.Phase = "Verified"
	nc.Status.Configuration.Operation.CredentialVersions = nil
	nc.Status.Configuration.CredentialApplications = nil
	if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, op.ID); err != errConfigurationBusy {
		t.Fatal("pruned credential operation released", err)
	}
}
