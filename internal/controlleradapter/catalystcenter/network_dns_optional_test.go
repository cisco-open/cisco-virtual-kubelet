// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package catalystcenter

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

func dnsOptionalModel(t *testing.T, mode, path string) []byte {
	t.Helper()
	var model map[string]any
	if err := json.Unmarshal(networkSingleSettingModel(t, "dns"), &model); err != nil {
		t.Fatal(err)
	}
	root := model["catalyst_center"].(map[string]any)
	profile := root["network_settings"].(map[string]any)["network"].([]any)[0].(map[string]any)
	profile["domain_name"] = "changed.example.test"
	delete(profile, "dns_servers")
	switch mode {
	case "null":
		profile["dns_servers"] = nil
		profile["domain_name"] = "null.example.test"
	case "clear":
		profile["dns_servers"] = []any{}
	case "match":
		profile["domain_name"] = "initial.example.test"
	}
	root["sites"] = map[string]any{"areas": []any{map[string]any{"name": lastSiteName(path), "parent_name": parentSitePath(path), "network_settings": map[string]any{"network": "base"}}}}
	raw, err := json.Marshal(model)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func exerciseDNSOptional(t *testing.T, mode string, install func(*adapter, *configapi.NetworkControllerConfig)) {
	t.Helper()
	ctx := context.Background()
	a, config := journalFixture(t)
	executor, native := networkSubmissionFixtureExecutor(t, "dns")
	a.client = executor.client
	initial := []string{"192.0.2.13", "192.0.2.14"}
	native.data["area/dns"] = map[string]any{"dns": map[string]any{"domainName": "initial.example.test", "dnsServers": initial}}
	if mode == "inherited" {
		delete(native.data, "area/dns")
		native.data["root/dns"] = map[string]any{"dns": map[string]any{"domainName": "initial.example.test", "dnsServers": initial}}
	}
	config.Spec.ManagedSections = []string{"network_settings"}
	config.Spec.Source.Inline = &runtime.RawExtension{Raw: dnsOptionalModel(t, mode, "Global/Lab")}
	if err := a.statusWriter.Update(ctx, config); err != nil {
		t.Fatal(err)
	}
	if install != nil {
		install(a, config)
	}
	req := ctrl.Request{NamespacedName: ctrlclient.ObjectKeyFromObject(config)}
	recovered := false
	for range 40 {
		restarted := *a
		if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
			t.Fatal(err)
		}
		state := journalState(t, a)
		if state != nil && state.Operation != nil && state.Operation.Phase == "Accepted" && !recovered {
			if err := a.advanceConfiguration(ctx, state.Operation.ID, "Unknown", ""); err != nil {
				t.Fatal(err)
			}
			recovered = true
		}
		if err := a.statusReader.Get(ctx, req.NamespacedName, config); err != nil {
			t.Fatal(err)
		}
		if config.Status.Phase == configapi.NetworkControllerConfigPhaseFailed {
			t.Fatal("DNS public apply failed")
		}
		if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync {
			continue
		}
		expected := 1
		if mode == "match" {
			expected = 0
		}
		if native.writes != expected || (expected == 1 && (!recovered || native.treeReads == 0)) {
			t.Fatal("missing original task recovery or unexpected mutation count")
		}
		records, err := networkSettingApplicationRecords(state)
		if err != nil || len(records) != expected {
			t.Fatal("wrong durable application count", err)
		}
		if expected == 1 {
			fields := native.body["dns"].(map[string]any)
			servers, present := fields["dnsServers"]
			if mode == "clear" {
				if present {
					t.Fatal("empty resolver reset must omit native dnsServers")
				}
			} else {
				raw, _ := json.Marshal(servers)
				var got []string
				_ = json.Unmarshal(raw, &got)
				if !reflect.DeepEqual(got, initial) {
					t.Fatal("unspecified resolvers were not preserved")
				}
			}
		}
		for range 2 {
			restarted := *a
			if _, err := (&configReconciler{a: &restarted}).Reconcile(ctx, req); err != nil {
				t.Fatal(err)
			}
		}
		if native.writes != expected || journalState(t, a).Operation != nil {
			t.Fatal("converged DNS replayed")
		}
		return
	}
	t.Fatal("DNS public lifecycle did not converge")
}

func TestNetworkDNSOptionalPublicLifecycle(t *testing.T) {
	for _, mode := range []string{"omitted", "null", "clear", "inherited", "match"} {
		t.Run(mode, func(t *testing.T) { exerciseDNSOptional(t, mode, nil) })
	}
}

func TestNetworkDNSOptionalPreflightPreservation(t *testing.T) {
	ctx := context.Background()
	e, f := networkSubmissionFixtureExecutor(t, "dns")
	bindings, err := parseNetworkSettings(dnsOptionalModel(t, "omitted", "Global/Lab"))
	if err != nil {
		t.Fatal(err)
	}
	e.bindings = bindings
	f.data["area/dns"] = map[string]any{"dns": map[string]any{"domainName": "initial.example.test", "dnsServers": []string{"192.0.2.13"}}}
	write, err := e.next(ctx)
	if err != nil || write == nil {
		t.Fatal("plan missing", err)
	}
	f.data["area/dns"] = map[string]any{"dns": map[string]any{"domainName": "initial.example.test", "dnsServers": []string{"192.0.2.99"}}}
	if _, err := write.submit(ctx); err == nil || f.writes != 0 {
		t.Fatal("stale preserved resolvers dispatched")
	}
	// The source stays domain-only; the payload was frozen from private native
	// observation, not inserted into source/status or an enlarged desired mask.
	if e.bindings[0].settings[0].servers != nil {
		t.Fatal("planning changed desired ownership")
	}
}

func TestNetworkDNSOptionalObservation(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"omitted", `{"domainName":"example.test"}`, true},
		{"null", `{"domainName":"example.test","dnsServers":null}`, true},
		{"empty", `{"domainName":"example.test","dnsServers":[]}`, true},
		{"wrong-type", `{"domainName":"example.test","dnsServers":"192.0.2.1"}`, false},
		{"invalid-address", `{"domainName":"example.test","dnsServers":["invalid"]}`, false},
		{"null-domain", `{"domainName":null}`, false},
		{"missing-domain", `{"dnsServers":[]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				fmt.Fprint(w, `{"response":{"dns":`+tc.body+`}}`)
			})
			got, err := c.readNetworkSetting(context.Background(), "area", "dns")
			if (err == nil) != tc.valid {
				t.Fatal("unexpected optional DNS response acceptance", err)
			}
			if tc.valid && (got.inherited || got.unset || got.domain != "example.test" || len(got.servers) != 0) {
				t.Fatal("invalid empty DNS observation")
			}
		})
	}
}

func TestNetworkDNSOptionalIntentMask(t *testing.T) {
	for _, mode := range []string{"omitted", "null", "clear"} {
		t.Run(mode, func(t *testing.T) {
			bindings, err := parseNetworkSettings(dnsOptionalModel(t, mode, "Global/Lab"))
			if err != nil {
				t.Fatal(err)
			}
			e := freezeNetworkExecutor(&networkConfigurationExecutor{bindings: bindings})
			setting := e.bindings[0].settings[0]
			if (setting.servers == nil) != (mode != "clear") {
				t.Fatal("freeze erased omission versus explicit empty")
			}
			drift := networkSettingDrift("Global/Lab", setting, networkObservation{domain: setting.domain, servers: []string{"192.0.2.99"}})
			if (len(drift) == 1) != (mode == "clear") {
				t.Fatal("unselected servers acquired desired ownership")
			}
		})
	}
}
