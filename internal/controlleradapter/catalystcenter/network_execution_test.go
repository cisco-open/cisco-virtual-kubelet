// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func allNetworkExecutionModel() []byte {
	return []byte(`{"catalyst_center":{"sites":{"global":{"network_settings":{"network":"base","telemetry":"base"}}},"network_settings":{"network":[{"name":"base","ntp_servers":["192.0.2.1"],"dhcp_servers":["192.0.2.2"],"dns_servers":["192.0.2.3","192.0.2.4"],"domain_name":"example.test","timezone":"Europe/Berlin","banner":"Authorized lab access"}],"telemetry":[` + telemetryProfile + `]}}}`)
}

func TestNetworkExecutionManifestConvergesAcrossRestarts(t *testing.T) {
	ctx := context.Background()
	a, config := journalFixture(t)
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: allNetworkExecutionModel()}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	settings := map[string]map[string]any{}
	tasks := map[string]int64{}
	writes := map[string]int{}
	a.client = testClient(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path == sitesPath && r.Method == "GET" {
			json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
			return
		}
		if strings.HasPrefix(r.URL.Path, taskPath) && r.Method == "GET" {
			id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, taskPath), "/tree")
			stamp, ok := tasks[id]
			if !ok {
				t.Error("unbound task")
				w.WriteHeader(404)
				return
			}
			if strings.HasSuffix(r.URL.Path, "/tree") {
				json.NewEncoder(w).Encode(map[string]any{"response": []any{map[string]any{"id": id, "rootId": id, "startTime": stamp, "endTime": stamp, "isError": false}}})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"id": id, "startTime": stamp, "endTime": stamp, "isError": false}})
			return
		}
		if !strings.HasPrefix(r.URL.Path, sitesPath+"/root/") {
			t.Error("wrong settings target")
			w.WriteHeader(404)
			return
		}
		kind := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, sitesPath+"/root/"), "Settings")
		if r.Method == "PUT" {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if kind == "telemetry" {
				av := body["applicationVisibility"].(map[string]any)
				if av["collector"] == nil || av["collectorType"] != nil {
					t.Error("collector payload was not nested")
				}
			}
			settings[kind] = body
			writes[kind]++
			id := "task-" + kind
			tasks[id] = time.Now().UnixMilli()
			json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"taskId": id}})
			return
		}
		if r.Method != "GET" || r.URL.Query().Get("_inherited") != "false" {
			t.Error("unexpected settings request")
		}
		body := settings[kind]
		if body == nil {
			body = map[string]any{kind: nil}
			if kind == "telemetry" {
				body = map[string]any{}
				for _, g := range telemetryGroups {
					body[g] = nil
				}
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"response": body})
	})
	key := ctrlclient.ObjectKeyFromObject(config)
	var current configapi.NetworkControllerConfig
	for range 45 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
			t.Fatal(err)
		}
		if err := a.statusReader.Get(ctx, key, &current); err != nil {
			t.Fatal(err)
		}
		if current.Status.Phase == configapi.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	if current.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
		t.Fatalf("not converged: %+v journal=%+v", current.Status, journalState(t, a))
	}
	if current.Status.SectionStatus[0].ResourceCount != 6 {
		t.Fatal("wrong settings resource count")
	}
	if _, err := (&configReconciler{a: a}).Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 6 {
		t.Fatalf("writes=%v", writes)
	}
	for kind, count := range writes {
		if count != 1 {
			t.Fatalf("replayed %s %d times", kind, count)
		}
	}
	if state := journalState(t, a); state.Operation != nil || len(state.Owners) != 1 || state.Owners[0].Section != "network_settings" {
		t.Fatalf("wrong ownership %+v", state)
	}
}

func TestNetworkExecutionPreflightAndCompleteObservation(t *testing.T) {
	for _, mode := range []string{"unsupported-aaa", "late-read-failure", "missing-site", "replaced-site"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				calls++
				if r.Method != "GET" {
					t.Error("unexpected mutation")
					w.WriteHeader(400)
					return
				}
				if r.URL.Path == sitesPath {
					sites := sampleSites()
					if mode == "missing-site" {
						sites = sites[:1]
					}
					if mode == "replaced-site" {
						sites[0].ID = "replacement"
					}
					json.NewEncoder(w).Encode(map[string]any{"response": sites})
					return
				}
				if strings.HasSuffix(r.URL.Path, "bannerSettings") {
					w.WriteHeader(500)
					return
				}
				kind := strings.TrimSuffix(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:], "Settings")
				json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{kind: nil}})
			})
			model := allNetworkExecutionModel()
			if mode == "unsupported-aaa" {
				model = []byte(`{"catalyst_center":{"sites":{"global":{"network_settings":{"aaa_servers":"a"}}},"network_settings":{"aaa_servers":[{"name":"a","network_aaa":{"server_type":"AAA","protocol":"RADIUS","primary_server_ip":"192.0.2.1"}}]}}}`)
			}
			if mode == "missing-site" {
				model = []byte(strings.Replace(networkReportModel, `"global":{"network_settings":{"network":"base"}}`, `"areas":[{"name":"Missing","parent_name":"Global","network_settings":{"network":"base"}}]`, 1))
			}
			e, err := c.compileConfigurationExecutor(model, "network_settings")
			if mode == "unsupported-aaa" {
				if err == nil || calls != 0 {
					t.Fatal("unsupported configuration reached observation")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if mode == "replaced-site" {
				_, err = e.verify(context.Background(), &ciskov1.NetworkControllerConfigurationOperation{Resource: "Global#ntp", Action: "Update", TargetID: "root"})
				if err == nil {
					t.Fatal("replaced site accepted")
				}
				return
			}
			if plan, err := e.next(context.Background()); err == nil || plan != nil {
				t.Fatal("partial observations produced a write plan")
			}
		})
	}
}

func TestNetworkPayloadUsesNativeResetAndResolverOrder(t *testing.T) {
	for _, setting := range []networkSetting{{kind: "ntp", servers: []string{}}, {kind: "dns", domain: "example.test", servers: []string{"192.0.2.2", "192.0.2.1"}}} {
		body, err := networkSettingPayload(setting)
		if err != nil {
			t.Fatal(err)
		}
		var parsed map[string]map[string]any
		if err = json.Unmarshal(body, &parsed); err != nil {
			t.Fatal(err)
		}
		if setting.kind == "ntp" {
			if value, present := parsed["ntp"]["servers"]; !present || value != nil {
				t.Fatal("explicit empty NTP reset did not use native null encoding")
			}
		} else if !reflect.DeepEqual(parsed["dns"]["dnsServers"], []any{"192.0.2.2", "192.0.2.1"}) {
			t.Fatal("DNS order changed")
		}
		if (len(networkSettingDrift("Global", setting, networkObservation{unset: true})) == 0) != (setting.kind == "ntp") {
			t.Fatal("unset convergence must be limited to the selected NTP reset")
		}
	}
}

func TestNetworkExecutionResolvesOnlyVerifiedAncestors(t *testing.T) {
	for _, mode := range []string{"inherited-match", "inherited-drift", "ancestor-failure", "wrong-parent"} {
		t.Run(mode, func(t *testing.T) {
			parentReads := 0
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if strings.Contains(r.URL.Path, "/area/") {
					fmt.Fprint(w, `{"response":{"ntp":null}}`)
					return
				}
				if strings.Contains(r.URL.Path, "/root/") {
					parentReads++
					if mode == "ancestor-failure" {
						w.WriteHeader(500)
						return
					}
					server := "192.0.2.1"
					if mode == "inherited-drift" {
						server = "192.0.2.2"
					}
					fmt.Fprintf(w, `{"response":{"ntp":{"servers":[%q]}}}`, server)
					return
				}
				t.Errorf("unexpected ancestry read %s", r.URL.Path)
			})
			sites := map[string]Site{}
			for _, site := range sampleSites() {
				sites[site.NameHierarchy] = site
			}
			if mode == "wrong-parent" {
				site := sites["Global/Lab"]
				site.ParentID = "foreign"
				sites["Global/Lab"] = site
			}
			got, err := c.effectiveNetworkSetting(context.Background(), sites, "Global/Lab", "ntp")
			if mode == "ancestor-failure" || mode == "wrong-parent" {
				if err == nil {
					t.Fatal("unverified ancestry accepted")
				}
				return
			}
			if err != nil || parentReads != 1 {
				t.Fatalf("reads=%d err=%v", parentReads, err)
			}
			drift := networkSettingDrift("Global/Lab", networkSetting{kind: "ntp", servers: []string{"192.0.2.1"}}, got)
			if (len(drift) == 0) != (mode == "inherited-match") {
				t.Fatalf("wrong inherited comparison: %+v", drift)
			}
		})
	}
}

func TestTelemetryExecutionCombinesExplicitAndInheritedGroups(t *testing.T) {
	bindings, err := parseNetworkSettings(telemetryModel(telemetryProfile))
	if err != nil {
		t.Fatal(err)
	}
	want := bindings[0].settings[0]
	body, err := networkSettingPayload(want)
	if err != nil {
		t.Fatal(err)
	}
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		var response map[string]any
		if err := json.Unmarshal(body, &response); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(r.URL.Path, "/area/") {
			response["wiredDataCollection"] = nil
			response["snmpTraps"] = nil
		}
		json.NewEncoder(w).Encode(map[string]any{"response": response})
	})
	sites := map[string]Site{}
	for _, site := range sampleSites() {
		sites[site.NameHierarchy] = site
	}
	got, err := c.effectiveNetworkSetting(context.Background(), sites, "Global/Lab", "telemetry")
	if err != nil {
		t.Fatal(err)
	}
	if drift := networkSettingDrift("Global/Lab", want, got); len(drift) != 0 {
		t.Fatalf("equivalent inherited telemetry drift: %+v", drift)
	}
}
