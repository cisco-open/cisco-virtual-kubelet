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
	ctrl "sigs.k8s.io/controller-runtime"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

const aaaProfile = `{"name":"base","network_aaa":{"server_type":"AAA","protocol":"TACACS","primary_ip":"192.0.2.10"},"client_and_endpoint_aaa":{"server_type":"ISE","protocol":"RADIUS","primary_ip":"192.0.2.20","pan":"192.0.2.30"}}`
const aaaResponse = `{"aaaNetwork":{"serverType":"AAA","protocol":"TACACS","primaryServerIp":"192.0.2.10","sharedSecret":"must-not-escape"},"aaaClient":{"serverType":"ISE","protocol":"RADIUS","primaryServerIp":"192.0.2.20","pan":"192.0.2.30","sharedSecret":"must-not-escape"}}`

func aaaModel(profile string) []byte {
	return []byte(`{"catalyst_center":{"sites":{"global":{"network_settings":{"aaa_servers":"base"}}},"network_settings":{"aaa_servers":[` + profile + `]}}}`)
}

func TestAAAReporting(t *testing.T) {
	for _, mode := range []string{"match", "changed", "inherited", "unset", "missing-group", "bad-type", "missing-pan", "null-primary", "partial"} {
		t.Run(mode, func(t *testing.T) {
			desired, err := parseNetworkSettings(aaaModel(aaaProfile))
			if err != nil {
				t.Fatal(err)
			}
			body := aaaResponse
			switch mode {
			case "changed":
				body = strings.Replace(body, "192.0.2.10", "192.0.2.11", 1)
			case "inherited":
				body = `{"aaaNetwork":null,"aaaClient":null}`
			case "unset":
				body = `{"aaaNetwork":{},"aaaClient":{}}`
			case "missing-group":
				body = `{"aaaNetwork":null}`
			case "bad-type":
				body = strings.Replace(body, `"TACACS"`, `"UNKNOWN"`, 1)
			case "missing-pan":
				body = strings.Replace(body, `,"pan":"192.0.2.30"`, ``, 1)
			case "null-primary":
				body = strings.Replace(body, `"primaryServerIp":"192.0.2.10"`, `"primaryServerIp":null`, 1)
			}
			c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if r.Method != "GET" {
					t.Error("unexpected mutation")
				}
				if r.URL.Path == sitesPath {
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
					return
				}
				if !strings.HasSuffix(r.URL.Path, "/aaaSettings") || r.URL.Query().Get("_inherited") != "false" {
					t.Error("wrong API")
				}
				if mode == "partial" {
					w.WriteHeader(206)
				}
				fmt.Fprint(w, `{"response":`+body+`}`)
			})
			drift, err := c.reportNetworkSettings(context.Background(), desired)
			valid := mode == "match" || mode == "changed" || mode == "inherited" || mode == "unset"
			if (err == nil) != valid {
				t.Fatalf("valid=%v error=%v", valid, err)
			}
			if !valid {
				if len(drift) != 0 {
					t.Fatal("partial drift escaped")
				}
				return
			}
			expected := 0
			if mode == "changed" {
				expected = 1
			}
			if mode == "inherited" || mode == "unset" {
				expected = 2
			}
			if len(drift) != expected {
				t.Fatalf("drift=%d", len(drift))
			}
			raw, _ := json.Marshal(drift)
			if strings.Contains(string(raw), "192.0.2") || strings.Contains(string(raw), "must-not-escape") {
				t.Fatal("values leaked")
			}
			observed, err := c.readNetworkSetting(context.Background(), "root", "aaa")
			if err != nil {
				t.Fatal(err)
			}
			raw, _ = json.Marshal(observed.aaa)
			if strings.Contains(string(raw), "must-not-escape") {
				t.Fatal("secret retained in observation")
			}
		})
	}
}
func TestAAAIntentRejected(t *testing.T) {
	for _, profile := range []string{
		strings.Replace(aaaProfile, `"protocol":"TACACS"`, `"protocol":null`, 1),
		strings.Replace(aaaProfile, `"protocol":"TACACS"`, `"protocol":"invalid"`, 1),
		strings.Replace(aaaProfile, `"primary_ip":"192.0.2.10"`, `"primary_ip":"hostname.invalid"`, 1),
		strings.Replace(aaaProfile, `,"pan":"192.0.2.30"`, ``, 1),
		strings.Replace(aaaProfile, `"protocol":"TACACS"`, `"protocol":"TACACS","shared_secret":"must-not-escape"`, 1),
		strings.Replace(aaaProfile, `"protocol":"TACACS"`, `"protocol":"TACACS","shared_secret_version":1`, 1),
	} {
		_, err := parseNetworkSettings(aaaModel(profile))
		if err == nil {
			t.Fatal("invalid metadata intent accepted")
		}
		if strings.Contains(err.Error(), "must-not-escape") {
			t.Fatal("secret in error")
		}
	}
}
func TestAAAConfigReportScope(t *testing.T) {
	for _, mode := range []string{"report", "secret", "apply"} {
		t.Run(mode, func(t *testing.T) {
			r, config, _ := reportFixture(t)
			ctx := context.Background()
			key := ctrlclient.ObjectKeyFromObject(config)
			if err := r.a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			config.Spec.ManagedSections = []string{"network_settings"}
			config.Spec.Source.Inline.Raw = aaaModel(aaaProfile)
			if mode == "secret" {
				config.Spec.Source.Inline.Raw = aaaModel(strings.Replace(aaaProfile, `"protocol":"TACACS"`, `"protocol":"TACACS","shared_secret":"must-not-escape"`, 1))
			}
			if mode == "apply" {
				config.Spec.Mode = configapi.NetworkControllerApplyModeApply
			}
			if err := r.a.statusWriter.Update(ctx, config); err != nil {
				t.Fatal(err)
			}
			calls := 0
			r.a.client = testClient(t, func(w http.ResponseWriter, req *http.Request) {
				calls++
				if req.URL.Path == authPath {
					fmt.Fprint(w, `{"Token":"test"}`)
					return
				}
				if req.URL.Path == sitesPath {
					json.NewEncoder(w).Encode(map[string]any{"response": sampleSites()})
					return
				}
				fmt.Fprint(w, `{"response":`+aaaResponse+`}`)
			})
			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
				t.Fatal(err)
			}
			if err := r.a.statusReader.Get(ctx, key, config); err != nil {
				t.Fatal(err)
			}
			raw, _ := json.Marshal(config.Status)
			if strings.Contains(string(raw), "must-not-escape") {
				t.Fatal("secret in status")
			}
			if mode == "report" {
				if config.Status.Phase != configapi.NetworkControllerConfigPhaseInSync || !strings.Contains(string(raw), "AAA shared secrets are not compared") {
					t.Fatal("metadata scope not explicit")
				}
			} else if calls != 0 || config.Status.Phase != configapi.NetworkControllerConfigPhaseFailed {
				t.Fatal("unsupported operation contacted API")
			}
		})
	}
}
