// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func noAuthCredentialReceipt(want globalCredentialIntent, owner, name string) cvk.NetworkControllerConfigurationActionApplication {
	submitted := metav1.NewTime(time.Now().Add(-2 * time.Minute).UTC().Truncate(time.Second))
	verified := metav1.NewTime(submitted.Add(time.Minute))
	return cvk.NetworkControllerConfigurationActionApplication{Section: "network_settings", Resource: want.metadataKey() + ":owned-snmp-id", VerifiedAt: &verified,
		Operation: cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigUID: owner, ConfigName: name, ConfigGeneration: 1, ControllerGeneration: 1, Section: "network_settings", Resource: want.metadataResource(), Phase: "Verified", Action: "Create", TaskID: "original-task", SubmittedAt: &submitted}}
}

func TestGlobalCredentialMetadataOwnershipAndModeBoundaries(t *testing.T) {
	for _, mode := range []string{"stable", "foreign-existing", "missing-owned", "replaced-owned", "foreign-receipt", "duplicate-receipt", "pruned-timestamp", "changed-native-mode", "versioned-owner", "to-auth", "same-name-cli"} {
		t.Run(mode, func(t *testing.T) {
			wants, _, err := splitNetworkCredentialConfiguration(noAuthCredentialModel("fixture", "tester"))
			if err != nil {
				t.Fatal(err)
			}
			want := wants[0]
			got := want.metadata
			got.ID = "owned-snmp-id"
			e := &globalCredentialExecutor{desired: wants, owner: "owner", configName: "config", metadataReceipts: []cvk.NetworkControllerConfigurationActionApplication{noAuthCredentialReceipt(want, "owner", "config")}}
			switch mode {
			case "foreign-existing":
				e.metadataReceipts = nil
			case "replaced-owned":
				got.ID = "replacement-id"
			case "foreign-receipt":
				e.metadataReceipts[0].Operation.ConfigUID = "foreign"
			case "duplicate-receipt":
				e.metadataReceipts = append(e.metadataReceipts, e.metadataReceipts[0])
			case "pruned-timestamp":
				e.metadataReceipts[0].VerifiedAt = nil
			case "changed-native-mode":
				got.Mode, got.AuthType = "AUTHNOPRIV", "SHA"
			case "versioned-owner":
				e.metadataReceipts = nil
				e.receipts = []cvk.NetworkControllerConfigurationCredentialApplication{{Section: "network_settings", Resource: want.resource(), ConfigUID: "owner", TargetID: got.ID}}
			case "to-auth":
				auth, secrets := globalCredentialFixture(t, "snmpv3_credentials")
				e.desired, e.secrets = []globalCredentialIntent{auth}, secrets
			case "same-name-cli":
				cli, secrets := globalCredentialFixture(t, "cli_credentials")
				e.desired, e.secrets = []globalCredentialIntent{cli, want}, secrets
			}
			e.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != http.MethodGet || r.URL.Path != globalCredentialsPath {
					t.Error("ownership preflight performed a mutation")
					http.NotFound(w, r)
					return
				}
				list := []globalCredentialMetadata{got}
				if mode == "missing-owned" {
					list = nil
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"snmpV3": list}})
			})
			write, err := e.next(context.Background())
			if mode == "stable" {
				if err != nil || write != nil {
					t.Fatal("owned metadata did not converge", err)
				}
			} else if mode == "to-auth" {
				if err != nil || write == nil || write.action != "Update" || write.identity.target != got.ID {
					t.Fatal("owned metadata could not promote its original UUID", err)
				}
			} else if mode == "same-name-cli" {
				if err != nil || write == nil || write.resource != e.desired[0].resource() {
					t.Fatal("independent credential kind was blocked", err)
				}
			} else if err == nil || write != nil {
				t.Fatal("ownership or mode boundary bypassed")
			}
		})
	}
}

func noAuthCredentialModel(name, username string) []byte {
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{"device_credentials": map[string]any{"snmpv3_credentials": []any{map[string]any{"name": name, "username": username, "snmp_mode": "NOAUTHNOPRIV"}}}}}})
	return raw
}

func noAuthCredentialConfigurationFixture(t *testing.T) (*adapter, *configapi.NetworkControllerConfig) {
	t.Helper()
	a, config, _, _ := credentialVersionJournalFixture(t)
	nc, err := a.currentController(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	nc.Spec.Endpoint = "https://catc.example"
	nc.Spec.CredentialSecretRef = cvk.NetworkControllerSecretReference{Name: "credentials"}
	nc.Spec.IntentSecretSources = nil
	if err := a.statusWriter.Update(context.Background(), nc); err != nil {
		t.Fatal(err)
	}
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.SecretRefs = nil
	config.Spec.ModelSource = configapi.NetworkControllerNetAsCodeModelSource{Format: "netascode-catalyst-center", ModelVersion: "0.5.0", Resolved: true}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: noAuthCredentialModel("fixture", "tester")}
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	return a, config
}

func noAuthCredentialTick(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
	t.Helper()
	restarted := *a
	key := ctrlclient.ObjectKeyFromObject(config)
	if _, err := (&configReconciler{a: &restarted}).Reconcile(context.Background(), ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	if err := a.statusReader.Get(context.Background(), key, config); err != nil {
		t.Fatal(err)
	}
}

func noAuthCredentialConverge(t *testing.T, a *adapter, config *configapi.NetworkControllerConfig) {
	t.Helper()
	for range 24 {
		noAuthCredentialTick(t, a, config)
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
			return
		}
	}
	t.Fatal("metadata credential did not converge", config.Status.Conditions)
}

func TestGlobalCredentialMetadataManifestRestartUpdateAndRepair(t *testing.T) {
	exerciseMetadataCredentialManifest(t, false)
}
func TestGlobalCredentialMetadataKnownTaskRecovery(t *testing.T) {
	exerciseMetadataCredentialManifest(t, true)
}
func exerciseMetadataCredentialManifest(t *testing.T, recoverTasks bool, installers ...func(*testing.T, *adapter, *configapi.NetworkControllerConfig)) {
	a, config := noAuthCredentialConfigurationFixture(t)
	var actual *globalCredentialMetadata
	writes := 0
	tasks := map[string]int64{}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == globalCredentialsPath && r.Method == http.MethodGet:
			list := []globalCredentialMetadata{}
			if actual != nil {
				list = append(list, *actual)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"snmpV3": list}})
		case r.URL.Path == globalCredentialsPath && (r.Method == http.MethodPost || r.Method == http.MethodPut):
			var body map[string]json.RawMessage
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
				t.Error("invalid metadata payload")
			}
			var payload map[string]any
			if r.Method == http.MethodPost {
				var list []map[string]any
				_ = json.Unmarshal(body["snmpV3"], &list)
				if len(list) != 1 || actual != nil {
					t.Error("repeated metadata create")
					w.WriteHeader(400)
					return
				}
				payload = list[0]
			} else {
				_ = json.Unmarshal(body["snmpV3"], &payload)
				if payload["id"] != "owned-snmp-id" {
					t.Error("wrong update identity")
				}
			}
			if len(payload) != 3 && len(payload) != 4 || payload["snmpMode"] != "NOAUTHNOPRIV" || payload["authPassword"] != nil || payload["privacyPassword"] != nil {
				t.Error("metadata write contains credential fields")
			}
			raw, _ := json.Marshal(payload)
			actual = &globalCredentialMetadata{}
			_ = json.Unmarshal(raw, actual)
			actual.ID = "owned-snmp-id"
			writes++
			id := fmt.Sprintf("metadata-task-%d", writes)
			tasks[id] = time.Now().UnixMilli()
			fmt.Fprintf(w, `{"response":{"taskId":%q}}`, id)
		case strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			ended, exists := tasks[id]
			if !exists {
				t.Error("uncorrelated task")
			}
			row := map[string]any{"id": id, "startTime": ended, "endTime": ended, "isError": false}
			var response any = row
			if strings.HasSuffix(r.URL.Path, "/tree") {
				response = []any{row}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": response})
		default:
			t.Errorf("unexpected metadata request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	for _, install := range installers {
		install(t, a, config)
	}
	recovered := 0
	converge := func() {
		t.Helper()
		marked := false
		for range 32 {
			noAuthCredentialTick(t, a, config)
			state := journalState(t, a)
			if recoverTasks && !marked && state.Operation != nil && state.Operation.Phase == "Accepted" {
				if err := a.advanceConfiguration(context.Background(), state.Operation.ID, "Unknown", ""); err != nil {
					t.Fatal(err)
				}
				marked = true
				recovered++
			}
			if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync && config.Status.ObservedGeneration == config.Generation {
				return
			}
		}
		t.Fatal("metadata credential did not converge", config.Status.Conditions)
	}
	converge()
	assertReceipt := func(expected int) {
		t.Helper()
		state := journalState(t, a)
		if writes != expected || state.Operation != nil || len(state.CredentialApplications) != 0 || len(state.ActionApplications) != 1 {
			t.Fatal("wrong metadata journal or write count", writes)
		}
		if id, err := metadataCredentialReceiptTarget(&state.ActionApplications[0], time.Now()); err != nil || id != "owned-snmp-id" {
			t.Fatal("missing exact retained UUID", err)
		}
	}
	assertReceipt(1)
	for range 3 {
		noAuthCredentialTick(t, a, config)
	}
	assertReceipt(1)
	config.Generation++
	config.Spec.Source.Inline.Raw = noAuthCredentialModel("fixture", "tester-updated")
	if err := a.statusWriter.Update(context.Background(), config); err != nil {
		t.Fatal(err)
	}
	converge()
	assertReceipt(2)
	actual.Username = "external-drift"
	converge()
	assertReceipt(3)
	if recoverTasks && recovered != 3 {
		t.Fatalf("recovered %d tasks, want 3", recovered)
	}
	// A same-name replacement cannot inherit the retained UUID's ownership.
	actual.ID = "replacement-id"
	for range 3 {
		noAuthCredentialTick(t, a, config)
	}
	assertReceipt(3)
	if config.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
		t.Fatal("replacement was adopted")
	}
}

func TestGlobalCredentialMetadataAtomicReceiptAndFences(t *testing.T) {
	for _, mode := range []string{"complete", "pruned", "generic-bypass", "foreign-owner", "stale-source", "failed-child", "running-child", "late-replacement", "mismatched-metadata", "future-task"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			a, config := noAuthCredentialConfigurationFixture(t)
			source, err := loadControllerSource(ctx, a.statusReader, config)
			if err != nil {
				t.Fatal(err)
			}
			wants, _, err := splitNetworkCredentialConfiguration(source.raw)
			if err != nil {
				t.Fatal(err)
			}
			op, err := a.prepareConfiguration(ctx, config, source, "network_settings", wants[0].metadataResource(), "Create")
			if err != nil {
				t.Fatal(err)
			}
			if err = a.startConfiguration(ctx, config, source, op.ID); err != nil {
				t.Fatal(err)
			}
			if err = a.advanceConfiguration(ctx, op.ID, "Accepted", "metadata-task"); err != nil {
				t.Fatal(err)
			}
			if err = a.advanceConfiguration(ctx, op.ID, "Verifying", ""); err != nil {
				t.Fatal(err)
			}
			state := journalState(t, a)
			submitted := state.Operation.SubmittedAt.Time
			if mode == "foreign-owner" {
				nc, _ := a.currentController(ctx)
				nc.Status.Configuration.Owners[0].ConfigUID = "foreign"
				if err := a.statusWriter.Status().Update(ctx, nc); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "stale-source" {
				changed := config.DeepCopy()
				changed.Spec.Source.Inline.Raw = noAuthCredentialModel("fixture", "changed-source")
				changed.Generation++
				if err := a.statusWriter.Update(ctx, changed); err != nil {
					t.Fatal(err)
				}
			}
			reads := 0
			a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case authPath:
					fmt.Fprint(w, `{"Token":"test"}`)
				case globalCredentialsPath:
					reads++
					got := wants[0].metadata
					got.ID = "owned-snmp-id"
					if mode == "late-replacement" && reads > 1 {
						got.ID = "replacement-id"
					}
					if mode == "mismatched-metadata" {
						got.Username = "different"
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"snmpV3": []any{got}}})
				case taskPath + "metadata-task/tree":
					ended := submitted.UnixMilli()
					if mode == "future-task" {
						ended = time.Now().Add(time.Second).UnixMilli()
					}
					root := map[string]any{"id": "metadata-task", "startTime": submitted.UnixMilli(), "endTime": ended, "isError": false}
					rows := []any{root}
					if mode == "failed-child" || mode == "running-child" {
						child := map[string]any{"id": "child", "parentId": "metadata-task", "rootId": "metadata-task", "startTime": submitted.UnixMilli(), "isError": mode == "failed-child"}
						rows = append(rows, child)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"response": rows})
				default:
					t.Error("unexpected verification request")
					http.NotFound(w, r)
				}
			})
			before := journalState(t, a).DeepCopy()
			if mode == "pruned" {
				a.statusWriter = pruningActionReceiptClient{Client: a.statusWriter}
			}
			if mode == "generic-bypass" {
				err = a.advanceConfiguration(ctx, op.ID, "Verified", "")
			} else {
				err = a.recordMetadataCredentialApplication(ctx, config, source, op.ID)
			}
			state = journalState(t, a)
			switch mode {
			case "complete":
				if err != nil || !metadataCredentialOperationReceiptPresent(state, state.Operation) || len(state.CredentialApplications) != 0 {
					t.Fatal("metadata completion not atomic", err)
				}
				if err := a.finishConfiguration(ctx, op.ID); err != nil {
					t.Fatal("receipt did not allow release", err)
				}
			case "pruned", "generic-bypass":
				if mode == "pruned" && err == nil {
					t.Fatal("receipt pruning was not detected")
				}
				if err := a.finishConfiguration(ctx, op.ID); err == nil {
					t.Fatal("missing receipt allowed release")
				}
			default:
				if err == nil || !reflect.DeepEqual(before, state) {
					t.Fatal("invalid completion altered journal", err)
				}
			}
		})
	}
}

func TestGlobalCredentialMetadataLostAcknowledgementKeepsFence(t *testing.T) {
	a, config := noAuthCredentialConfigurationFixture(t)
	writes := 0
	var actual []globalCredentialMetadata
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path != globalCredentialsPath {
			t.Error("uncertain metadata operation attempted another endpoint")
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"snmpV3": actual}})
			return
		}
		writes++
		actual = []globalCredentialMetadata{{ID: "created-without-ack", Name: "fixture", Username: "tester", Mode: "NOAUTHNOPRIV"}}
		fmt.Fprint(w, `{"response":{}}`)
	})
	for range 5 {
		noAuthCredentialTick(t, a, config)
	}
	state := journalState(t, a)
	if writes != 1 || state.Operation == nil || len(state.ActionApplications) != 0 || len(state.CredentialApplications) != 0 {
		t.Fatal("uncertain metadata operation was replayed, released or adopted")
	}
	if err := a.finishConfiguration(context.Background(), state.Operation.ID); err == nil {
		t.Fatal("uncertain metadata operation released")
	}
}
