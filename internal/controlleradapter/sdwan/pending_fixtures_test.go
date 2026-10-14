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
	for fixture, count := range map[string]int{"transport-management-vpn": 2, "transport-management-interface": 3, "service-lan-vpn": 2, "service-dual-router-ha": 3, "service-lan-vpn-ospf-association": 4, "transport-wan-vpn-ospf-association": 4, "service-lan-vpn-eigrp-association": 4, "service-lan-vpn-multicast-association": 4, "service-lan-vpn-ospfv3-associations": 7, "service-lan-vpn-svi": 3, "service-lan-vpn-svi-dhcp-association": 5, "service-lan-vpn-ethernet": 3, "service-lan-vpn-ethernet-dhcp-association": 5, "service-lan-vpn-ethernet-tracker-association": 5, "service-lan-vpn-ethernet-tracker-group-association": 7, "service-lan-vpn-gre": 3, "transport-wan-vpn-gre": 3, "transport-wan-vpn-gre-tracker-association": 5, "service-lan-vpn-ipsec": 3, "transport-wan-vpn-ipsec": 3, "transport-wan-vpn-ipsec-tracker-association": 5, "transport-wan-vpn-ethernet": 3, "transport-wan-vpn-cellular": 3, "topology-custom-control": 4, "topology-group": 2, "policy-group": 4, "application-priority-qos": 3, "application-priority-traffic": 3, "embedded-security-ngfw": 3, "embedded-security-policy": 3, "classic-ipv4-acl-definition": 1, "classic-ipv6-acl-definition": 1, "classic-ipv4-device-acl-definition": 1, "classic-ipv6-device-acl-definition": 1, "classic-route-policy-definition": 1, "classic-qos-rewrite-definitions": 4, "classic-centralized-membership-mesh": 7, "classic-centralized-cflowd": 1, "classic-centralized-traffic-data": 1, "classic-centralized-app-route": 1, "classic-security-ips": 4, "service-lan-vpn-ethernet-member": 3, "transport-wan-vpn-ethernet-member": 3, "service-lan-vpn-bgp-association": 4, "transport-wan-vpn-bgp-association": 4, "transport-wan-vpn-cellular-tracker-association": 5, "transport-wan-vpn-cellular-tracker-group-association": 7, "transport-wan-vpn-ethernet-ipv4-tracker-association": 5, "transport-wan-vpn-ethernet-ipv4-tracker-group-association": 7, "transport-wan-vpn-ethernet-ipv6-tracker-association": 5, "transport-wan-vpn-ethernet-ipv6-tracker-group-association": 7, "transport-wan-vpn": 2, "policy-prefixes": 8, "transport-trackers": 7, "policy-security": 12, "policy-dependencies": 6, "ca-certificate": 2, "cisco-sse": 2, "classic-sig-credentials": 1, "classic-security": 1, "classic-ospf": 1, "routing-ospf": 6, "classic-svi": 1, "classic-cellular-interface": 1, "classic-switchport": 1, "routing-eigrp": 2, "zscaler-sse": 2, "service-switchport": 2, "custom-applications": 1, "service-multicast": 2, "system-device-access": 3, "system-device-access-references": 6, "topology-mesh": 4, "topology-hubspoke": 6, "ipv4-acls": 4, "ipv4-acl-references": 6, "ipv6-acls": 4, "ipv6-acl-references": 6, "route-policies": 4, "route-policy-references": 5} {
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

func TestClassicQoSRewriteClassMapDependencies(t *testing.T) {
	resources := parcelFixtures(t, "classic-qos-rewrite-definitions")
	if len(resources) != 4 || resources[0].Type != "class" || resources[1].Type != "qos_map_policy_definition" || resources[2].Type != "rewrite_rule_policy_definition" || resources[3].Type != "localized_policy" {
		t.Fatal("class map must precede both dependent definitions")
	}
	for _, r := range resources[1:3] {
		if len(r.ProfileRefs) != 1 || r.ProfileRefs["0"] != resources[0].Name {
			t.Fatal("definition lost its class-map reference", r.Type)
		}
	}
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-qos-rewrite-definitions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	missing := strings.Replace(string(raw), "class_maps:\n      - name: CVK-CLASSIC-QOS-CLASS\n        queue: 2", "class_maps: []", 1)
	if _, err := Compile([]byte(missing)); err == nil {
		t.Fatal("undeclared class map accepted")
	}
}

func TestCentralizedDefinitionPolicyListDependencies(t *testing.T) {
	resources := parcelFixtures(t, "classic-centralized-membership-mesh")
	if len(resources) != 7 {
		t.Fatal("expected three lists, three definitions and policy assembly")
	}
	seen := map[string]bool{}
	for _, r := range resources {
		if r.Kind == "classic-definition" {
			if !seen["site"] || !seen["vpn"] || len(r.PolicyListRefs) < 2 {
				t.Fatal("centralized definition must follow declared lists", r.Type)
			}
		}
		if r.Kind == "centralized-policy" && (!seen["mesh_topology_policy_definition"] || !seen["vpn_membership_policy_definition"] || !seen["hub_and_spoke_topology_policy_definition"]) {
			t.Fatal("policy assembly must follow all definitions")
		}
		seen[r.Type] = true
	}
	raw, err := os.ReadFile("../../../examples/configs/sdwan/classic-centralized-membership-mesh.yaml")
	if err != nil {
		t.Fatal(err)
	}
	missing := strings.Replace(string(raw), "vpn_lists:\n      - name: CVK-CENTRAL-VPNS\n        vpn_ids: [10]", "vpn_lists: []", 1)
	if _, err := Compile([]byte(missing)); err == nil {
		t.Fatal("undeclared VPN list accepted")
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

func TestTopologyMeshSiteModes(t *testing.T) {
	resources := parcelFixtures(t, "topology-mesh")
	for i, r := range resources {
		if r.Type != "topology_mesh" {
			continue
		}
		if i != len(resources)-1 || len(r.HierarchySites) != 1 || r.HierarchySites[0] != "CVK-MESH-SITE" {
			t.Fatal("site group expansion or dependency order changed")
		}
		data := r.Body["data"].(map[string]any)
		if data["hierarchyUuid"] == nil || data["sites"] != nil {
			t.Fatal("20.18+ should use hierarchy UUIDs")
		}
	}
	raw, err := os.ReadFile("../../../examples/configs/sdwan/topology-mesh.yaml")
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(raw), "sdwan:\n", "sdwan:\n  manager_version: 20.15\n", 1)
	legacy = strings.Replace(legacy, "site_groups: [CVK-MESH-GROUP]", "sites: [CVK-MESH-SITE]", 1)
	compiled, err := Compile([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range compiled {
		if r.Type == "topology_mesh" {
			data := r.Body["data"].(map[string]any)
			if data["sites"] == nil || data["hierarchyUuid"] != nil {
				t.Fatal("legacy site-name mode changed")
			}
		}
	}
	bad := strings.Replace(string(raw), "CVK-MESH-GROUP]", "MISSING]", 1)
	if _, err := Compile([]byte(bad)); err == nil {
		t.Fatal("missing site group accepted")
	}
}

func TestTopologyHubSpokeSiteModes(t *testing.T) {
	resources := parcelFixtures(t, "topology-hubspoke")
	var policy Resource
	for _, r := range resources {
		if r.Type == "topology_hubspoke" {
			policy = r
		}
	}
	if policy.HubSpoke == nil || !policy.HubSpoke.UUIDMode || len(policy.HubSpoke.Spokes) != 1 || len(policy.HubSpoke.Spokes[0].Sites) != 1 || policy.HubSpoke.Spokes[0].Sites[0] != "CVK-SPOKE-SITE" {
		t.Fatal("hub-spoke group expansion changed")
	}
	data := policy.Body["data"].(map[string]any)
	if data["selectedHierarchyHubs"] == nil || data["selectedHubs"] != nil {
		t.Fatal("20.18+ must use hierarchy UUIDs")
	}
	raw, err := os.ReadFile("../../../examples/configs/sdwan/topology-hubspoke.yaml")
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.Replace(string(raw), "sdwan:\n", "sdwan:\n  manager_version: 20.15\n", 1)
	compiled, err := Compile([]byte(legacy))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range compiled {
		if r.Type == "topology_hubspoke" {
			data := r.Body["data"].(map[string]any)
			if data["selectedHubs"] == nil || data["selectedHierarchyHubs"] != nil {
				t.Fatal("legacy site-name mode changed")
			}
		}
	}
	bad := strings.Replace(string(raw), "spoke_site_groups: [CVK-SPOKE-GROUP]", "spoke_site_groups: [MISSING]", 1)
	if _, err := Compile([]byte(bad)); err == nil {
		t.Fatal("unknown spoke group accepted")
	}
}

func TestIPv4ACLReferencesAndValidation(t *testing.T) {
	resources := parcelFixtures(t, "ipv4-acl-references")
	count := 0
	for _, r := range resources {
		if r.Type != "service_ipv4_acl" {
			continue
		}
		if len(r.ParcelRefs) != 3 {
			t.Fatal("expected prefix, mirror and policer references")
		}
		for _, ref := range r.ParcelRefs {
			if ref.ParentName != "CVK-ACL-OBJECTS" || ref.Collection != "/dataservice/v1/feature-profile/sdwan/policy-object" {
				t.Fatal("wrong ACL reference parent", ref)
			}
		}
		count++
	}
	if count != 1 {
		t.Fatal("missing service IPv4 ACL")
	}
	bad := map[string]any{"name": "bad", "default_action": "drop", "sequences": []any{map[string]any{"id": float64(10), "base_action": "accept", "match_entries": map[string]any{"source_data_prefix": "192.0.2.0/24", "source_data_prefix_list": "named"}}}}
	if _, _, _, err := ipv4ACLData(bad, "service"); err == nil {
		t.Fatal("conflicting ACL prefixes accepted")
	}
	transport := map[string]any{"name": "chain", "default_action": "drop", "sequences": []any{map[string]any{"id": float64(10), "base_action": "accept", "actions": map[string]any{"service_chain_name": "sc1", "service_chain_vpn": float64(10)}}}}
	_, body, _, err := ipv4ACLData(transport, "transport")
	if err != nil {
		t.Fatal(err)
	}
	sequence := body["data"].(map[string]any)["sequences"].([]any)[0].(map[string]any)
	chain := sequence["actions"].([]any)[0].(map[string]any)["accept"].(map[string]any)["setServiceChain"].(map[string]any)
	if chain["serviceChainNumber"].(map[string]any)["value"] != "SC1" {
		t.Fatal("transport service chain case mapping changed")
	}
}

func TestIPv6ACLReferencesAndValidation(t *testing.T) {
	resources := parcelFixtures(t, "ipv6-acl-references")
	count := 0
	for _, r := range resources {
		if r.Type != "service_ipv6_acl" {
			continue
		}
		if len(r.ParcelRefs) != 3 {
			t.Fatal("expected IPv6 prefix, mirror and policer references")
		}
		for _, ref := range r.ParcelRefs {
			if ref.ParentName != "CVK-ACL-OBJECTS" || ref.Collection != "/dataservice/v1/feature-profile/sdwan/policy-object" {
				t.Fatal("wrong IPv6 ACL reference parent", ref)
			}
		}
		if r.ParcelRefs[0].Kind != "policy_object_data_ipv6_prefix_list" && r.ParcelRefs[1].Kind != "policy_object_data_ipv6_prefix_list" && r.ParcelRefs[2].Kind != "policy_object_data_ipv6_prefix_list" {
			t.Fatal("IPv6 prefix reference missing")
		}
		count++
	}
	if count != 1 {
		t.Fatal("missing service IPv6 ACL")
	}
	bad := map[string]any{"name": "bad", "default_action": "drop", "sequences": []any{map[string]any{"id": float64(10), "base_action": "accept", "match_entries": map[string]any{"source_data_prefix": "2001:db8::/32", "source_data_prefix_list": "named"}}}}
	if _, _, _, err := ipv6ACLData(bad, "service"); err == nil {
		t.Fatal("conflicting IPv6 ACL prefixes accepted")
	}
}

func TestRoutePolicyReferencesAndOrigin(t *testing.T) {
	resources := parcelFixtures(t, "route-policy-references")
	count := 0
	for _, r := range resources {
		if r.Type != "service_route_policy" {
			continue
		}
		if len(r.ParcelRefs) != 2 {
			t.Fatal("route policy references missing")
		}
		for _, ref := range r.ParcelRefs {
			if ref.ParentName != "CVK-ROUTE-OBJECTS" || ref.Collection != "/dataservice/v1/feature-profile/sdwan/policy-object" {
				t.Fatal("route policy reference target changed", ref)
			}
		}
		count++
	}
	if count != 1 {
		t.Fatal("missing route policy")
	}
	input := map[string]any{"name": "origin", "sequences": []any{map[string]any{"id": float64(10), "base_action": "accept", "actions": map[string]any{"origin": "incomplete"}}}}
	_, body, _, err := routePolicyData(input, "transport")
	if err != nil {
		t.Fatal(err)
	}
	seq := body["data"].(map[string]any)["sequences"].([]any)[0].(map[string]any)
	origin := seq["actions"].([]any)[0].(map[string]any)["accept"].(map[string]any)["setOrigin"].(map[string]any)["value"]
	if origin != "Incomplete" {
		t.Fatal("origin translation changed", origin)
	}
}

// These opt-in entry points exercise native Manager APIs only. They are not
// executed by the normal local suite and do not imply live qualification.
func TestLivePendingNamesAbsent(t *testing.T) {
	for _, fixture := range []string{"transport-trackers", "policy-prefixes", "policy-dependencies", "policy-security", "ca-certificate", "cisco-sse", "classic-sig-credentials", "classic-security", "classic-ospf", "routing-ospf", "classic-svi", "classic-cellular-interface", "classic-switchport", "routing-eigrp", "zscaler-sse", "service-switchport", "custom-applications", "service-multicast", "system-device-access", "topology-mesh", "topology-hubspoke", "ipv4-acls", "ipv6-acls", "route-policies", "transport-management-vpn", "transport-wan-vpn", "transport-management-interface", "service-lan-vpn", "service-dual-router-ha", "service-lan-vpn-ospf-association", "transport-wan-vpn-ospf-association", "service-lan-vpn-eigrp-association", "service-lan-vpn-multicast-association", "service-lan-vpn-ospfv3-associations", "service-lan-vpn-svi", "service-lan-vpn-svi-dhcp-association", "service-lan-vpn-ethernet", "service-lan-vpn-ethernet-dhcp-association", "service-lan-vpn-ethernet-tracker-association", "service-lan-vpn-ethernet-tracker-group-association", "service-lan-vpn-gre", "transport-wan-vpn-gre", "transport-wan-vpn-gre-tracker-association", "service-lan-vpn-ipsec", "transport-wan-vpn-ipsec", "transport-wan-vpn-ipsec-tracker-association", "transport-wan-vpn-ethernet", "transport-wan-vpn-ethernet-ipv4-tracker-association", "transport-wan-vpn-ethernet-ipv4-tracker-group-association", "transport-wan-vpn-ethernet-ipv6-tracker-association", "transport-wan-vpn-ethernet-ipv6-tracker-group-association", "transport-wan-vpn-cellular", "transport-wan-vpn-cellular-tracker-association", "transport-wan-vpn-cellular-tracker-group-association", "service-lan-vpn-bgp-association", "transport-wan-vpn-bgp-association", "service-lan-vpn-ethernet-member", "transport-wan-vpn-ethernet-member", "topology-custom-control", "topology-group", "policy-group", "application-priority-qos", "application-priority-traffic", "embedded-security-ngfw", "embedded-security-policy", "classic-ipv4-acl-definition", "classic-ipv6-acl-definition", "classic-ipv4-device-acl-definition", "classic-ipv6-device-acl-definition", "classic-route-policy-definition", "classic-qos-rewrite-definitions", "classic-centralized-membership-mesh", "classic-centralized-cflowd", "classic-centralized-traffic-data", "classic-centralized-app-route", "classic-security-ips"} {
		t.Run(fixture, func(t *testing.T) { liveParcelNamesAbsent(t, parcelFixtures(t, fixture)) })
	}
}
func TestLivePendingLifecycle(t *testing.T) {
	fixture := os.Getenv("CVK_SDWAN_PENDING_FIXTURE")
	switch fixture {
	case "transport-trackers", "policy-prefixes", "policy-dependencies", "policy-security", "ca-certificate", "cisco-sse", "classic-sig-credentials", "classic-security", "classic-ospf", "routing-ospf", "classic-svi", "classic-cellular-interface", "classic-switchport", "routing-eigrp", "zscaler-sse", "service-switchport", "custom-applications", "service-multicast", "system-device-access", "topology-mesh", "topology-hubspoke", "ipv4-acls", "ipv6-acls", "route-policies", "transport-management-vpn", "transport-wan-vpn", "transport-management-interface", "service-lan-vpn", "service-dual-router-ha", "service-lan-vpn-ospf-association", "transport-wan-vpn-ospf-association", "service-lan-vpn-eigrp-association", "service-lan-vpn-multicast-association", "service-lan-vpn-ospfv3-associations", "service-lan-vpn-svi", "service-lan-vpn-svi-dhcp-association", "service-lan-vpn-ethernet", "service-lan-vpn-ethernet-dhcp-association", "service-lan-vpn-ethernet-tracker-association", "service-lan-vpn-ethernet-tracker-group-association", "service-lan-vpn-gre", "transport-wan-vpn-gre", "transport-wan-vpn-gre-tracker-association", "service-lan-vpn-ipsec", "transport-wan-vpn-ipsec", "transport-wan-vpn-ipsec-tracker-association", "transport-wan-vpn-ethernet", "transport-wan-vpn-ethernet-ipv4-tracker-association", "transport-wan-vpn-ethernet-ipv4-tracker-group-association", "transport-wan-vpn-ethernet-ipv6-tracker-association", "transport-wan-vpn-ethernet-ipv6-tracker-group-association", "transport-wan-vpn-cellular", "transport-wan-vpn-cellular-tracker-association", "transport-wan-vpn-cellular-tracker-group-association", "service-lan-vpn-bgp-association", "transport-wan-vpn-bgp-association", "service-lan-vpn-ethernet-member", "transport-wan-vpn-ethernet-member", "topology-custom-control", "topology-group", "policy-group", "application-priority-qos", "application-priority-traffic", "embedded-security-ngfw", "embedded-security-policy", "classic-ipv4-acl-definition", "classic-ipv6-acl-definition", "classic-ipv4-device-acl-definition", "classic-ipv6-device-acl-definition", "classic-route-policy-definition", "classic-qos-rewrite-definitions", "classic-centralized-membership-mesh", "classic-centralized-cflowd", "classic-centralized-traffic-data", "classic-centralized-app-route", "classic-security-ips":
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
