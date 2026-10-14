// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

func TestPolicyReferenceChains(t *testing.T) {
	for _, fixture := range []string{"policy-dependencies", "policy-security"} {
		seen := map[string]bool{}
		for _, r := range parcelFixtures(t, fixture) {
			for _, ref := range r.ParcelRefs {
				if !seen[ref.ParcelPath+"/"+ref.Name] {
					t.Fatalf("%s precedes %s", r.Name, ref.Name)
				}
			}
			seen[r.ParcelPath+"/"+r.Name] = true
		}
	}
	raw, err := os.ReadFile("../../../examples/configs/sdwan/policy-dependencies.yaml")
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(raw), "forwarding_class: CVK-CLASS", "forwarding_class: EXTERNAL", 1)
	if _, err := Compile([]byte(bad)); err == nil {
		t.Fatal("undeclared reference accepted")
	}
	a := Resource{Kind: "parcel", Name: "a", Collection: "c", ParentName: "p", ParcelPath: "path", ParcelRefs: []ParcelReference{{Name: "b", ParcelPath: "path"}}}
	b := a
	b.Name = "b"
	b.ParcelRefs = []ParcelReference{{Name: "a", ParcelPath: "path"}}
	if _, err := orderParcelDependencies([]Resource{a, b}); err == nil {
		t.Fatal("cycle accepted")
	}
}

func TestPolicyReferenceMissingDependencyDoesNotWrite(t *testing.T) {
	var probe Resource
	for _, r := range parcelFixtures(t, "policy-dependencies") {
		if r.Type == "policy_object_app_probe_class" {
			probe = r
		}
	}
	present := false
	writes := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			return
		}
		if r.Method != "GET" {
			writes++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			encoded, _ := json.Marshal(body)
			if !strings.Contains(string(encoded), "11111111-1111-4111-8111-111111111111") {
				t.Error("unresolved reference in write")
			}
			fmt.Fprint(w, `{"parcelId":"probe-id"}`)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/policy-object"):
			fmt.Fprint(w, `[{"profileId":"profile-id","profileName":"CVK-POLICY-DEPENDENCIES"}]`)
		case strings.HasSuffix(r.URL.Path, "/class"):
			if present {
				fmt.Fprint(w, `{"data":[{"parcelId":"11111111-1111-4111-8111-111111111111","name":"CVK-CLASS"}]}`)
			} else {
				fmt.Fprint(w, `{"data":[]}`)
			}
		default:
			fmt.Fprint(w, `{"payload":{}}`)
		}
	})
	if _, err := c.Create(context.Background(), probe); !errors.Is(err, ErrDependencyPending) || writes != 0 {
		t.Fatal("missing dependency permitted write", err, writes)
	}
	present = true
	if _, err := c.Create(context.Background(), probe); err != nil || writes != 1 {
		t.Fatal("create failed", err, writes)
	}
}

func TestDeviceAccessCrossProfileReferenceResolution(t *testing.T) {
	var access Resource
	for _, r := range parcelFixtures(t, "system-device-access-references") {
		if r.Type == "system_ipv4_device_access" {
			access = r
		}
	}
	present := false
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if auth(w, r) {
			return
		}
		if r.Method != "GET" {
			t.Fatal("unexpected mutation", r.Method)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/policy-object"):
			fmt.Fprint(w, `[{"profileId":"profile-id","profileName":"CVK-DEVICE-ACCESS-OBJECTS"}]`)
		case strings.HasSuffix(r.URL.Path, "/data-prefix"):
			if present {
				fmt.Fprint(w, `{"data":[{"parcelId":"11111111-1111-4111-8111-111111111111","name":"CVK-SSH-V4-SOURCES"}]}`)
			} else {
				fmt.Fprint(w, `{"data":[]}`)
			}
		case strings.HasSuffix(r.URL.Path, "/data-prefix/11111111-1111-4111-8111-111111111111"):
			fmt.Fprint(w, `{"payload":{"name":"CVK-SSH-V4-SOURCES","description":"","data":{}}}`)
		default:
			t.Error("wrong collection", r.URL.Path)
		}
	})
	if err := c.resolveParcelRefs(context.Background(), access); !errors.Is(err, ErrDependencyPending) {
		t.Fatal("missing prefix accepted", err)
	}
	present = true
	if err := c.resolveParcelRefs(context.Background(), access); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(access.Body)
	if !strings.Contains(string(encoded), "11111111-1111-4111-8111-111111111111") {
		t.Fatal("reference ID not substituted")
	}
}

func TestPolicyCombinedArraysAndConditions(t *testing.T) {
	for _, r := range parcelFixtures(t, "policy-security") {
		d, ok := r.Body["data"].(map[string]any)
		if !ok {
			continue
		}
		if r.Type == "policy_object_application_list" || r.Type == "policy_object_security_local_application_list" {
			entries := d["entries"].([]any)
			if len(entries) != 2 {
				t.Fatal("application family lost")
			}
			a := entries[0].(map[string]any)
			b := entries[1].(map[string]any)
			if a["app"].(map[string]any)["value"] != "http" || b["appFamily"].(map[string]any)["value"] != "web" {
				t.Fatal("combined source order/mapping changed")
			}
		}
		if r.Type == "policy_object_unified_url_filtering" && d["blockPageContents"].(map[string]any)["value"] != "Access to the requested page has been denied. Contact the lab operator." {
			t.Fatal("block page text changed")
		}
	}
	base := map[string]any{"name": "amp", "alert_log_level": "critical", "amp_cloud_region": "nam", "file_analysis": false, "tg_cloud_region": "nam"}
	_, d, err := scalarParcelData("policy_object_unified_advanced_malware_protection", base)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := d["fileAnalysisCloudServer"]; exists {
		t.Fatal("inactive file-analysis branch emitted")
	}
	if _, _, err := scalarParcelData("policy_object_security_zone", map[string]any{"name": "zone", "vpns": []any{"10"}, "interfaces": []any{"GigabitEthernet1"}}); err == nil {
		t.Fatal("mixed zone modes accepted")
	}
	if _, _, err := scalarParcelData("policy_object_application_list", map[string]any{"name": "apps", "entries": []any{}}); err == nil {
		t.Fatal("internal combined field exposed")
	}
}

func TestReferenceCollectionCannotBeSubstituted(t *testing.T) {
	raw, err := os.ReadFile("../../../examples/configs/sdwan/policy-security.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// Allow and block lists share a Manager endpoint; their public collections
	// are nevertheless distinct upstream references.
	raw = []byte(strings.Replace(string(raw), "security_url_allow_lists:", "security_url_block_lists:", 1))
	if _, err := Compile(raw); err == nil {
		t.Fatal("reference resolved through the wrong public collection")
	}
}

func TestTransportTrackerVariants(t *testing.T) {
	for _, r := range parcelFixtures(t, "transport-trackers") {
		if !strings.Contains(r.Name, "HTTP") && !strings.Contains(r.Name, "ICMP") {
			continue
		}
		d := r.Body["data"].(map[string]any)
		if strings.Contains(r.Name, "HTTP") {
			if _, exists := d["icmpInterval"]; exists {
				t.Fatal("HTTP tracker emitted ICMP interval")
			}
			if d["interval"].(map[string]any)["value"] != float64(60) {
				t.Fatal("HTTP interval changed")
			}
		} else {
			if _, exists := d["interval"]; exists {
				t.Fatal("ICMP tracker emitted HTTP interval")
			}
			if strings.Contains(r.Name, "TRACKER6") {
				if d["endpointIp"].(map[string]any)["value"] != "2001:db8::1" {
					t.Fatal("endpoint alias lost")
				}
				if d["icmpInterval"].(map[string]any)["value"] != "{{icmp6_interval}}" {
					t.Fatal("ICMP interval variable lost")
				}
			} else if d["icmpInterval"].(map[string]any)["value"] != float64(2) {
				t.Fatal("ICMP interval changed")
			}
		}
	}
	input := map[string]any{"name": "tracker", "tracker_name": "tracker", "endpoint_api_url": "http://example.com"}
	_, d, err := scalarParcelData("transport_tracker", input)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := d["icmpInterval"]; exists {
		t.Fatal("default tracker emitted ICMP interval")
	}
	if d["interval"].(map[string]any)["value"] != float64(60) {
		t.Fatal("default HTTP interval changed")
	}
}
