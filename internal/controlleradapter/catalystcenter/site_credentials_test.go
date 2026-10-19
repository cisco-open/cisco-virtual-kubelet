// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter/catalystcenter/nacmodel"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func siteCredentialModel(value any) []byte {
	site := map[string]any{"name": "Lab", "parent_name": "Global"}
	for _, k := range globalCredentialKinds {
		site[k.model] = value
	}
	raw, _ := json.Marshal(map[string]any{"catalyst_center": map[string]any{"network_settings": map[string]any{}, "sites": map[string]any{"areas": []any{site}}}})
	return raw
}
func TestSiteCredentialParsing(t *testing.T) {
	for _, value := range []any{"example", nil} {
		intent, err := parseNetworkReportIntent(siteCredentialModel(value))
		if err != nil || len(intent.assignments) != 6 {
			t.Fatalf("assignments=%d err=%v", len(intent.assignments), err)
		}
	}
	for _, value := range []any{"", 1, map[string]any{}, []any{"example"}} {
		if _, err := parseNetworkReportIntent(siteCredentialModel(value)); err == nil {
			t.Fatalf("accepted invalid reference %#v", value)
		}
	}
}
func TestSiteCredentialManifestRestartAndRemovalGate(t *testing.T) {
	for _, global := range []bool{false, true} {
		t.Run(fmt.Sprint("global=", global), func(t *testing.T) { siteCredentialManifestCycle(t, global) })
	}
}

type siteCredentialManifestQualification struct {
	recover  bool
	selected bool
	install  func(*adapter, *configapi.NetworkControllerConfig)
}

func siteCredentialManifestCycle(t *testing.T, global bool, qualification ...siteCredentialManifestQualification) {
	model := siteCredentialModel("example")
	siteID := "area"
	if global {
		model = globalSiteCredentialModel("example")
		siteID = "root"
	}
	if len(qualification) > 0 && qualification[0].selected {
		var err error
		model, _, err = ExportSelectedCredentials(model, nil, nacmodel.SiteSelectionOptions{ManagedSites: []string{"Global/Lab"}, ManageSpecificSitesOnly: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"network_settings"}
	recovery := len(qualification) > 0 && qualification[0].recover
	if recovery {
		config.Spec.TaskTimeout = &metav1.Duration{Duration: time.Nanosecond}
	}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: model}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if len(qualification) > 0 && qualification[0].install != nil {
		qualification[0].install(a, config)
	}
	var mu sync.Mutex
	slots := map[string]any{}
	for _, k := range globalCredentialKinds {
		slots[credentialSlot(k.model)] = nil
	}
	tasks := map[string]int64{}
	writes := 0
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.URL.Path == authPath:
			fmt.Fprint(w, `{"Token":"test"}`)
		case r.URL.Path == sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
		case r.URL.Path == globalCredentialsPath:
			response := map[string]any{}
			for _, k := range globalCredentialKinds {
				response[k.api] = []any{map[string]any{"id": "id-" + k.api, "description": "example"}}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": response})
		case strings.HasPrefix(r.URL.Path, taskPath):
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, ok := tasks[id]
			if !ok {
				t.Error("unbound task")
			}
			row := map[string]any{"id": id, "startTime": stamp, "endTime": stamp, "isError": false}
			var response any = row
			if strings.HasSuffix(r.URL.Path, "/tree") {
				response = []any{row}
			}
			json.NewEncoder(w).Encode(map[string]any{"response": response})
		case r.URL.Path == siteCredentialsPath(siteID) && r.Method == "GET":
			if r.URL.Query().Get("_inherited") != "false" {
				t.Error("effective read")
			}
			json.NewEncoder(w).Encode(map[string]any{"response": slots})
		case r.URL.Path == siteCredentialsPath(siteID) && r.Method == "PUT":
			var body map[string]map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || len(body) != 1 {
				t.Error("not one explicit slot")
				w.WriteHeader(400)
				return
			}
			for slot, value := range body {
				if len(value) != 1 {
					t.Error("invalid slot")
				}
				if value["credentialsId"] == nil {
					slots[slot] = nil
				} else {
					slots[slot] = value
				}
			}
			writes++
			id := fmt.Sprintf("task-%d", writes)
			tasks[id] = time.Now().UnixMilli()
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	recovered := map[string]string{}
	converge := func() {
		t.Helper()
		var current configapi.NetworkControllerConfig
		for range 70 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := a.statusReader.Get(ctx, key, &current); err != nil {
				t.Fatal(err)
			}
			if recovery {
				state := journalState(t, a)
				if state != nil && state.Operation != nil && state.Operation.Phase == "Unknown" {
					op := state.Operation
					if old, ok := recovered[op.Resource]; ok && old != op.TaskID {
						t.Fatal("acknowledged assignment task replaced")
					}
					recovered[op.Resource] = op.TaskID
				}
			}
			if current.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
				return
			}
		}
		t.Fatalf("did not converge: %+v journal=%+v", current.Status, journalState(t, a))
	}
	converge()
	converge()
	mu.Lock()
	got := writes
	mu.Unlock()
	if got != 6 || recovery && len(recovered) != 6 {
		t.Fatalf("writes=%d recovered=%d", got, len(recovered))
	}
	if err := a.statusReader.Get(ctx, key, config); err != nil {
		t.Fatal(err)
	}
	config.Generation++
	removal := siteCredentialModel(nil)
	path, kind := "Global/Lab", "area"
	if global {
		removal = globalSiteCredentialModel(nil)
		path, kind = "Global", "global"
	}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: removal}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if _, err := a.client.compileConfigurationExecutor(removal, "network_settings", nil); err == nil {
		t.Fatal("unsafe inheritance removal accepted")
	}
	e := &siteCredentialExecutor{client: a.client, desired: []siteCredentialIntent{{site: path, siteKind: kind, kind: "cli_credentials"}}}
	if write, err := e.next(ctx); err == nil || write != nil {
		t.Fatal("native removal bypassed gate")
	}
	mu.Lock()
	defer mu.Unlock()
	if writes != 6 {
		t.Fatalf("removal submitted writes=%d", writes)
	}
}
func TestSiteCredentialReadAndDependencyFailures(t *testing.T) {
	for _, mode := range []string{"partial", "disabled", "inherited", "wrong-type", "replacement", "missing-credential"} {
		t.Run(mode, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Fatal("unexpected mutation")
				}
				switch r.URL.Path {
				case sitesPath:
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
				case globalCredentialsPath:
					response := map[string]any{}
					if mode != "missing-credential" {
						response["cliCredential"] = []any{map[string]any{"id": "new-id", "description": "example"}}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": response})
				case siteCredentialsPath("area"):
					response := map[string]any{}
					for _, k := range globalCredentialKinds {
						response[credentialSlot(k.model)] = nil
					}
					switch mode {
					case "partial":
						delete(response, "httpWriteCredentialsId")
					case "disabled":
						response["cliCredentialsId"] = map[string]any{}
					case "inherited":
						response["cliCredentialsId"] = map[string]any{"credentialsId": "new-id", "inheritedSiteId": "root"}
					case "wrong-type":
						response["cliCredentialsId"] = true
					default:
						response["cliCredentialsId"] = map[string]any{"credentialsId": "new-id"}
					}
					json.NewEncoder(w).Encode(map[string]any{"response": response})
				default:
					w.WriteHeader(404)
				}
			})
			desired := siteCredentialIntent{site: "Global/Lab", siteKind: "area", kind: "cli_credentials", name: "example"}
			e := &siteCredentialExecutor{client: c, desired: []siteCredentialIntent{desired}}
			if mode == "replacement" {
				ok, err := e.verify(context.Background(), &ciskov1.NetworkControllerConfigurationOperation{Resource: desired.resource(), Action: "Update", TargetID: "area", ParentID: "old-id"})
				if err == nil || ok {
					t.Fatal("accepted replacement credential")
				}
				return
			}
			m, err := e.next(context.Background())
			if mode == "disabled" {
				if err != nil || m == nil {
					t.Fatal("unset confused with desired credential", err)
				}
				return
			}
			if err == nil || m != nil {
				t.Fatal("accepted incomplete observation")
			}
		})
	}
}

func TestSiteCredentialReportProjectionAndPreflight(t *testing.T) {
	raw := []byte(`{"catalyst_center":{"network_settings":{},"sites":{"areas":[{"name":"Lab","parent_name":"Global","cli_credentials":"example"},{"name":"Missing","parent_name":"Global","cli_credentials":"absent"}]}}}`)
	intent, err := parseNetworkReportIntent(raw)
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.Method != "GET" {
			t.Error("mutated before complete dependency validation")
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case sitesPath:
			json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
		case globalCredentialsPath:
			fmt.Fprint(w, `{"response":{"cliCredential":[{"id":"credential","description":"example"}]}}`)
		case siteCredentialsPath("area"):
			slots := map[string]any{}
			for _, k := range globalCredentialKinds {
				slots[credentialSlot(k.model)] = nil
			}
			json.NewEncoder(w).Encode(map[string]any{"response": slots})
		default:
			w.WriteHeader(404)
		}
	})
	e := &siteCredentialExecutor{client: c, desired: intent.assignments}
	if write, err := e.next(context.Background()); err == nil || write != nil {
		t.Fatal("partial plan escaped missing dependency")
	}
	// Report projections must retain assignment references in network_settings
	// while excluding them from the site geometry parser.
	_, err = c.compileConfigReports(raw, []string{"sites", "network_settings"})
	if err != nil {
		t.Fatal("combined report projection failed", err)
	}
}
