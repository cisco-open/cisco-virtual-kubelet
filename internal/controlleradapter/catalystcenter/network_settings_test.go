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
	"testing"

	configapi "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const networkReportModel = `{"catalyst_center":{"sites":{"global":{"network_settings":{"network":"base"}}},"network_settings":{"network":[{"name":"base","ntp_servers":["192.0.2.1"]}]}}}`

func TestNetworkSettingsModel(t *testing.T) {
	for _, tc := range []struct {
		name, model string
		valid       bool
	}{
		{"valid", networkReportModel, true},
		{"undefined", strings.Replace(networkReportModel, `"network":"base"`, `"network":"missing"`, 1), false},
		{"unsupported", strings.Replace(networkReportModel, `"ntp_servers"`, `"unsupported_field"`, 1), false},
		{"null", strings.Replace(networkReportModel, `["192.0.2.1"]`, `null`, 1), true},
		{"duplicate", strings.Replace(networkReportModel, `["192.0.2.1"]`, `["192.0.2.1","192.0.2.1"]`, 1), false},
		{"dns-unselected", strings.Replace(networkReportModel, `"ntp_servers"`, `"dns_servers"`, 1), true},
		{"dns", strings.Replace(networkReportModel, `"ntp_servers"`, `"domain_name":"example.test","dns_servers"`, 1), true},
		{"unused", strings.Replace(networkReportModel, `"name":"base"`, `"name":"base"`, 1) + "\n---\n{}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseNetworkSettings([]byte(tc.model))
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
		})
	}
}

func TestNetworkSettingsObservation(t *testing.T) {
	for _, tc := range []struct {
		name, body       string
		status           int
		valid, inherited bool
		drift            int
	}{
		{"explicit", `{"ntp":{"servers":["192.0.2.1"]}}`, 200, true, false, 0},
		{"inherited", `{"ntp":null}`, 200, true, true, 1},
		{"unset", `{"ntp":{}}`, 200, true, false, 1},
		{"missing", `{}`, 200, false, false, 0},
		{"partial", `{"ntp":{"servers":["192.0.2.1"]}}`, 206, false, false, 0},
		{"null-servers", `{"ntp":{"servers":null}}`, 200, false, false, 0},
		{"wrong-type", `{"ntp":{"servers":"192.0.2.1"}}`, 200, false, false, 0},
		{"effective-inheritance", `{"ntp":{"servers":["192.0.2.1"],"inheritedSiteId":"parent"}}`, 200, false, false, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" || r.URL.Path != sitesPath+"/root/ntpSettings" || r.URL.Query().Get("_inherited") != "false" {
					t.Error("unexpected request")
				}
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"response":`+tc.body+`}`)
			})
			got, err := c.readNetworkSetting(context.Background(), "root", "ntp")
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if !tc.valid {
				return
			}
			if got.inherited != tc.inherited {
				t.Fatal("inheritance lost")
			}
			drift := networkSettingDrift("Global", networkSetting{kind: "ntp", servers: []string{"192.0.2.1"}}, got)
			if len(drift) != tc.drift {
				t.Fatalf("drift count %d", len(drift))
			}
			raw, _ := json.Marshal(drift)
			if strings.Contains(string(raw), "192.0.2.1") {
				t.Fatal("status leaks values")
			}
		})
	}
}

func TestNetworkSettingsConfigReport(t *testing.T) {
	for _, mode := range []string{"match", "inherited", "failure", "apply", "malformed"} {
		t.Run(mode, func(t *testing.T) {
			r, config, _ := reportFixture(t)
			ctx := context.Background()
			key := ctrlclient.ObjectKeyFromObject(config)
			if err := r.a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			config.Spec.ManagedSections = []string{"network_settings"}
			config.Spec.Source.Inline.Raw = []byte(networkReportModel)
			if mode == "apply" {
				config.Spec.Mode = configapi.NetworkControllerApplyModeApply
			}
			if mode == "malformed" {
				config.Spec.Source.Inline.Raw = []byte(`{}`)
			}
			if err := r.a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			reads := 0
			r.a.client = testClient(t, func(w http.ResponseWriter, req *http.Request) {
				if req.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				reads++
				if req.Method != "GET" {
					t.Fatal("configuration mutation")
				}
				if req.URL.Path == sitesPath {
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
					return
				}
				if mode == "failure" {
					w.WriteHeader(500)
					fmt.Fprint(w, "private error")
					return
				}
				if mode == "inherited" {
					fmt.Fprint(w, `{"response":{"ntp":null}}`)
					return
				}
				fmt.Fprint(w, `{"response":{"ntp":{"servers":["192.0.2.1"]}}}`)
			})
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := r.a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			want := configapi.NetworkControllerConfigPhaseFailed
			if mode == "match" || mode == "apply" {
				want = configapi.NetworkControllerConfigPhaseInSync
			}
			if mode == "inherited" {
				want = configapi.NetworkControllerConfigPhaseDrifted
			}
			if config.Status.Phase != want {
				t.Fatalf("phase %s: %+v", config.Status.Phase, config.Status.Conditions)
			}
			if mode == "malformed" && reads != 0 {
				t.Fatal("unsupported intent contacted API")
			}
			if mode == "failure" && (len(config.Status.Drift) != 0 || config.Status.LastControllerCheck != nil) {
				t.Fatal("partial report published")
			}
		})
	}
}

func TestNetworkSettingsOrderingAndAtomicFailure(t *testing.T) {
	for _, kind := range []string{"ntp", "dhcp", "dns"} {
		a, err := parseServerList([]any{"192.0.2.2", "192.0.2.1"}, kind)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := parseServerList([]any{"192.0.2.1", "192.0.2.2"}, kind)
		drift := networkSettingDrift("Global", networkSetting{kind: kind, servers: a}, networkObservation{servers: b})
		if (len(drift) > 0) != (kind == "dns") {
			t.Fatalf("%s order semantics incorrect", kind)
		}
	}
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == authPath {
			fmt.Fprint(w, `{"Token":"test"}`)
			return
		}
		if r.URL.Path == sitesPath {
			json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
			return
		}
		calls++
		if calls == 1 {
			fmt.Fprint(w, `{"response":{"ntp":null}}`)
			return
		}
		w.WriteHeader(500)
	})
	drift, err := c.reportNetworkSettings(context.Background(), []networkBinding{{path: "Global", kind: "global", settings: []networkSetting{{kind: "ntp"}, {kind: "dhcp"}}}})
	if err == nil || len(drift) != 0 || calls != 2 {
		t.Fatal("partial observations escaped failure boundary")
	}
}

func TestNetworkSettingsDNSAndDHCP(t *testing.T) {
	for _, tc := range []struct {
		kind, body string
		valid      bool
	}{
		{"dns", `{"domainName":"example.test","dnsServers":["192.0.2.2","192.0.2.1"]}`, true},
		{"dns", `{"dnsServers":["192.0.2.1"]}`, false},
		{"dns", `{"domainName":"example.test","dnsServers":null}`, true},
		{"dhcp", `{"servers":["192.0.2.2","192.0.2.1"]}`, true},
		{"dhcp", `{"servers":["dhcp.example.test"]}`, false},
	} {
		t.Run(tc.kind+tc.body, func(t *testing.T) {
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.URL.Path != sitesPath+"/root/"+tc.kind+"Settings" {
					t.Error("wrong endpoint")
				}
				fmt.Fprintf(w, `{"response":{"%s":%s}}`, tc.kind, tc.body)
			})
			got, err := c.readNetworkSetting(context.Background(), "root", tc.kind)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v error=%v", tc.valid, err)
			}
			if tc.valid && tc.kind == "dns" {
				expected := []string{"192.0.2.2", "192.0.2.1"}
				if strings.Contains(tc.body, `"dnsServers":null`) {
					expected = []string{}
				}
				if got.domain != "example.test" || !reflect.DeepEqual(got.servers, expected) {
					t.Fatal("DNS identity/order or empty semantics lost")
				}
			}
		})
	}
}
