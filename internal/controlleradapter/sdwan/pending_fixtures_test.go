// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"encoding/json"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestPendingNativeFixtures(t *testing.T) {
	for fixture, count := range map[string]int{"policy-prefixes": 8, "transport-trackers": 7, "policy-security": 12, "policy-dependencies": 6, "ca-certificate": 2, "cisco-sse": 2, "classic-sig-credentials": 1, "classic-security": 1, "classic-ospf": 1, "routing-ospf": 6, "classic-svi": 1, "classic-cellular-interface": 1, "classic-switchport": 1, "routing-eigrp": 2, "zscaler-sse": 2, "service-switchport": 2, "custom-applications": 1, "service-multicast": 2, "system-device-access": 3, "system-device-access-references": 6} {
		t.Run(fixture, func(t *testing.T) {
			rs := parcelFixtures(t, fixture)
			if len(rs) != count {
				t.Fatalf("got %d resources, want %d", len(rs), count)
			}
			raw, err := os.ReadFile("../../../examples/configs/sdwan/" + fixture + ".yaml")
			if err != nil {
				t.Fatal(err)
			}
			resolved, err := Resolve(raw)
			if err != nil {
				t.Fatal(err)
			}
			again, err := Compile(resolved)
			if err != nil || !reflect.DeepEqual(rs, again) {
				t.Fatal("resolve changed intent", err)
			}
		})
	}
}

func TestPolicyPrefixEncodingAndAMPFanout(t *testing.T) {
	for _, r := range parcelFixtures(t, "policy-prefixes") {
		data, ok := r.Body["data"].(map[string]any)
		if !ok {
			continue
		}
		switch r.Type {
		case "policy_object_data_ipv4_prefix_list":
			e := data["entries"].([]any)[0].(map[string]any)
			if e["ipv4Address"].(map[string]any)["value"] != "192.0.2.0" || e["ipv4PrefixLength"].(map[string]any)["value"] != float64(24) {
				t.Fatal("prefix split changed", data)
			}
		case "policy_object_unified_advanced_malware_protection":
			for _, key := range []string{"fileReputationCloudServer", "fileReputationEstServer"} {
				if data[key].(map[string]any)["value"] != "nam" {
					t.Fatal("shared source mapping lost", key)
				}
			}
		}
	}
	for _, prefix := range []string{"192.0.2.0/33", "2001:db8::/32", "192.0.2.1", "bad/24"} {
		input := map[string]any{"name": "bad", "prefixes": []any{prefix}}
		if _, _, err := scalarParcelData("policy_object_data_ipv4_prefix_list", input); err == nil {
			t.Fatal("invalid IPv4 prefix accepted", prefix)
		}
	}
	var schema map[string]any
	_ = json.Unmarshal([]byte(`{"type":"object","minProperties":1,"maxProperties":2}`), &schema)
	if validScalarValue(map[string]any{}, schema) || validScalarValue(map[string]any{"a": 1, "b": 2, "c": 3}, schema) {
		t.Fatal("object bounds ignored")
	}
}

func TestSystemDeviceAccessPolicyReferences(t *testing.T) {
	resources := parcelFixtures(t, "system-device-access-references")
	count := 0
	for _, r := range resources {
		if r.Type != "system_ipv4_device_access" && r.Type != "system_ipv6_device_access" {
			continue
		}
		if len(r.ParcelRefs) != 1 {
			t.Fatal("expected one prefix-list reference", r.Type)
		}
		ref := r.ParcelRefs[0]
		if ref.Collection != "/dataservice/v1/feature-profile/sdwan/policy-object" || ref.ParentName != "CVK-DEVICE-ACCESS-OBJECTS" {
			t.Fatal("reference target changed", ref)
		}
		count++
	}
	if count != 2 {
		t.Fatal("expected both device-access families")
	}
	input := map[string]any{"default_action": "drop", "sequences": []any{map[string]any{"id": float64(1), "base_action": "accept", "match_entries": map[string]any{"destination_port": float64(22), "source_data_prefixes": []any{"192.0.2.0/24"}, "source_data_prefix_list": "named"}}}}
	if _, _, err := deviceAccessData(input, "ipv4"); err == nil {
		t.Fatal("conflicting inline and referenced prefixes accepted")
	}
}

// These opt-in entry points exercise native Manager APIs only. They are not
// executed by the normal local suite and do not imply live qualification.
func TestLivePendingNamesAbsent(t *testing.T) {
	for _, fixture := range []string{"transport-trackers", "policy-prefixes", "policy-dependencies", "policy-security", "ca-certificate", "cisco-sse", "classic-sig-credentials", "classic-security", "classic-ospf", "routing-ospf", "classic-svi", "classic-cellular-interface", "classic-switchport", "routing-eigrp", "zscaler-sse", "service-switchport", "custom-applications", "service-multicast", "system-device-access"} {
		t.Run(fixture, func(t *testing.T) { liveParcelNamesAbsent(t, parcelFixtures(t, fixture)) })
	}
}
func TestLivePendingLifecycle(t *testing.T) {
	fixture := os.Getenv("CVK_SDWAN_PENDING_FIXTURE")
	switch fixture {
	case "transport-trackers", "policy-prefixes", "policy-dependencies", "policy-security", "ca-certificate", "cisco-sse", "classic-sig-credentials", "classic-security", "classic-ospf", "routing-ospf", "classic-svi", "classic-cellular-interface", "classic-switchport", "routing-eigrp", "zscaler-sse", "service-switchport", "custom-applications", "service-multicast", "system-device-access":
		liveParcelLifecycle(t, parcelFixtures(t, fixture))
	default:
		t.Skip("select a pending fixture with CVK_SDWAN_PENDING_FIXTURE")
	}
}

func TestUnqualifiedInventoryHasNativeFixtures(t *testing.T) {
	var lock struct {
		Resources []struct {
			Type, Implementation string
			Fixtures             []string
			LiveTested           bool `json:"live_tested"`
			LocalTested          bool `json:"local_tested"`
		}
	}
	raw, err := os.ReadFile("testdata/upstream-lock.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &lock); err != nil {
		t.Fatal(err)
	}
	cached := map[string][]Resource{}
	for _, row := range lock.Resources {
		if row.Implementation != "implemented-unqualified" {
			continue
		}
		if row.LiveTested || !row.LocalTested || len(row.Fixtures) == 0 {
			t.Fatal("invalid qualification evidence", row.Type)
		}
		matched := false
		for _, fixture := range row.Fixtures {
			rs, ok := cached[fixture]
			if !ok {
				rs = parcelFixtures(t, fixture)
				cached[fixture] = rs
			}
			for _, r := range rs {
				kind := strings.TrimPrefix(row.Type, "sdwan_")
				if r.Type == kind {
					matched = true
				}
				switch row.Type {
				case "sdwan_policy_object_feature_profile":
					matched = matched || (r.Kind == "profile" && r.Type == "policy-object")
				case "sdwan_cisco_sig_credentials_feature_template":
					matched = matched || (r.Kind == "" && r.Type == "cisco_sig_credentials")
				case "sdwan_system_ca_certificate_feature":
					matched = matched || (r.Collection == "/dataservice/v1/feature-profile/sdwan/system" && r.ParcelPath == "ca-cert")
				case "sdwan_sse_cisco_feature":
					matched = matched || (r.Collection == "/dataservice/v1/feature-profile/sdwan/sse" && r.ParcelPath == "cisco")
				default:
					paths := map[string]string{"sdwan_transport_tracker_feature": "tracker", "sdwan_transport_ipv6_tracker_feature": "ipv6-tracker", "sdwan_transport_tracker_group_feature": "trackergroup", "sdwan_transport_ipv6_tracker_group_feature": "ipv6-trackergroup"}
					if path, ok := paths[row.Type]; ok {
						matched = matched || (r.Collection == "/dataservice/v1/feature-profile/sdwan/transport" && r.ParcelPath == path)
					}
				}
			}
		}
		if !matched {
			t.Fatal("inventory declaration lacks a native compiled fixture", row.Type)
		}
	}
}
