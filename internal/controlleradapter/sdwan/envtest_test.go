//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package sdwan

import (
	"context"
	"encoding/json"
	"github.com/cisco/virtual-kubelet-cisco/internal/controlleradapter"
	coordinationv1 "k8s.io/api/coordination/v1"
	"os"
	"testing"

	configv1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	cvkv1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/yaml"
)

func TestEnvtestSDWANReconciliation(t *testing.T) {
	api := &fixtureAPI{}
	a, cfg := testAdapter(t, api)
	env := &envtest.Environment{CRDDirectoryPaths: []string{"../../../config/crd"}, ErrorIfCRDPathMissing: true}
	rest, e := env.Start()
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		if e := env.Stop(); e != nil {
			t.Error(e)
		}
	})
	k, e := client.New(rest, client.Options{Scheme: testScheme()})
	if e != nil {
		t.Fatal(e)
	}
	ctx := context.Background()
	if e = k.Create(ctx, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "lab"}}); e != nil {
		t.Fatal(e)
	}
	nc := a.opts.Controller.DeepCopy()
	nc.UID = ""
	nc.ResourceVersion = ""
	nc.Generation = 0
	if os.Getenv("CVK_SDWAN_ENVTEST_LIVE") == "1" {
		a.remote = liveClient(t)
		a.remote.Guard = a.guard
		nc.Spec.Endpoint = a.remote.Endpoint
		nc.Spec.TLS = &cvkv1.NetworkControllerTLSConfig{InsecureSkipVerify: true}
		fixture := os.Getenv("CVK_SDWAN_ENVTEST_FIXTURE")
		if fixture == "" {
			fixture = "banner.yaml"
		}

		sections := map[string][]string{
			"custom-applications.yaml":                 {"application_catalog"},
			"banner.yaml":                              {"edge_feature_templates"},
			"system-profile.yaml":                      {"feature_profiles", "configuration_groups"},
			"policy-objects.yaml":                      {"policy_objects"},
			"deployment.yaml":                          {"network_hierarchy", "policy_objects", "edge_feature_templates", "feature_profiles", "configuration_groups"},
			"topology-mesh.yaml":                       {"network_hierarchy", "feature_profiles"},
			"topology-hubspoke.yaml":                   {"network_hierarchy", "feature_profiles"},
			"topology-custom-control.yaml":             {"network_hierarchy", "feature_profiles"},
			"topology-group.yaml":                      {"feature_profiles", "topology_groups"},
			"policy-group.yaml":                        {"feature_profiles", "policy_groups"},
			"application-priority-qos.yaml":            {"feature_profiles"},
			"application-priority-traffic.yaml":        {"feature_profiles"},
			"embedded-security-ngfw.yaml":              {"feature_profiles"},
			"embedded-security-policy.yaml":            {"feature_profiles"},
			"classic-ipv4-acl-definition.yaml":         {"localized_policies"},
			"classic-ipv6-acl-definition.yaml":         {"localized_policies"},
			"classic-ipv4-device-acl-definition.yaml":  {"localized_policies"},
			"classic-ipv6-device-acl-definition.yaml":  {"localized_policies"},
			"classic-route-policy-definition.yaml":     {"localized_policies"},
			"classic-qos-rewrite-definitions.yaml":     {"policy_objects", "localized_policies"},
			"classic-centralized-membership-mesh.yaml": {"policy_objects", "centralized_policies"},
			"classic-centralized-cflowd.yaml":          {"centralized_policies"},
			"classic-centralized-traffic-data.yaml":    {"centralized_policies"},
			"classic-centralized-custom-control.yaml":  {"centralized_policies"},
			"classic-centralized-app-route.yaml":       {"centralized_policies"},
			"classic-security-ips.yaml":                {"policy_objects", "security_policies"},
			"network-hierarchy-collectors.yaml":        {"network_hierarchy"},
		}
		for _, name := range []string{"system-features", "peripheral-features", "thousandeyes-feature", "snmp-feature", "aaa-feature", "service-features", "transport-cellular", "transport-trackers", "routing-ospf", "routing-eigrp", "zscaler-sse", "service-switchport", "service-multicast", "system-device-access", "ipv4-acls", "ipv6-acls", "route-policies", "transport-management-vpn", "transport-wan-vpn", "transport-management-interface", "service-lan-vpn", "service-dual-router-ha", "service-lan-vpn-ospf-association", "transport-wan-vpn-ospf-association", "service-lan-vpn-eigrp-association", "service-lan-vpn-multicast-association", "service-lan-vpn-ospfv3-associations", "service-lan-vpn-svi", "service-lan-vpn-svi-dhcp-association", "service-lan-vpn-ethernet", "service-lan-vpn-ethernet-dhcp-association", "service-lan-vpn-ethernet-tracker-association", "service-lan-vpn-ethernet-tracker-group-association", "service-lan-vpn-gre", "transport-wan-vpn-gre", "transport-wan-vpn-gre-tracker-association", "service-lan-vpn-ipsec", "transport-wan-vpn-ipsec", "transport-wan-vpn-ipsec-tracker-association", "transport-wan-vpn-ethernet", "transport-wan-vpn-ethernet-ipv4-tracker-association", "transport-wan-vpn-ethernet-ipv4-tracker-group-association", "transport-wan-vpn-ethernet-ipv6-tracker-association", "transport-wan-vpn-ethernet-ipv6-tracker-group-association", "transport-wan-vpn-cellular", "transport-wan-vpn-cellular-tracker-association", "transport-wan-vpn-cellular-tracker-group-association", "service-lan-vpn-bgp-association", "transport-wan-vpn-bgp-association", "service-lan-vpn-ethernet-member", "transport-wan-vpn-ethernet-member", "policy-profile", "policy-prefixes", "policy-dependencies", "policy-security", "ca-certificate", "cisco-sse"} {
			sections[name+".yaml"] = []string{"feature_profiles"}
		}
		for _, name := range []string{"templates", "cellular", "bfd", "igmp", "ntp", "omp", "pim", "applications", "dhcp", "ipsec", "snmp", "logging", "gre", "sig-credentials", "security", "ospf", "svi", "cellular-interface", "switchport"} {
			sections["classic-"+name+".yaml"] = []string{"edge_feature_templates"}
		}
		managed, ok := sections[fixture]
		if !ok {
			t.Fatal("unsupported fixture")
		}
		cfg.Spec.ManagedSections = managed
		b, e := os.ReadFile("../../../examples/configs/sdwan/" + fixture)
		if e != nil {
			t.Fatal(e)
		}
		cfg.Spec.Source.Inline.Raw, e = yaml.YAMLToJSON(b)
		if e != nil {
			t.Fatal(e)
		}
		rs, e := Compile(cfg.Spec.Source.Inline.Raw)
		if e != nil {
			t.Fatal(e)
		}
		savedGuard := a.remote.Guard
		a.remote.Guard = nil
		for _, r := range rs {
			existing, e := a.remote.Observe(ctx, r)
			if e != nil || existing != nil {
				t.Fatal("live fixture name must be absent", r.Name, e)
			}
		}
		a.remote.Guard = savedGuard

		t.Cleanup(func() {
			a.remote.Guard = nil
			for i := len(rs) - 1; i >= 0; i-- {
				r := rs[i]
				lock := &controlleradapter.SectionLease{ControllerUID: string(nc.UID), Scope: "global", Section: r.Section}
				lease := &coordinationv1.Lease{}
				if e := k.Get(ctx, client.ObjectKey{Namespace: "lab", Name: lock.Name()}, lease); e != nil {
					continue
				}
				journal := controlleradapter.SectionJournal{}
				if json.Unmarshal([]byte(lease.Annotations["cisco.vk/controller-section-journal"]), &journal) != nil {
					t.Error("invalid fixture journal")
					continue
				}
				receipt := journal[resourceKey(r)]
				if receipt.ID == "" {
					continue
				}
				if e := a.remote.DeleteResourceFixture(ctx, r, receipt.ID); e != nil {
					t.Error("fixture cleanup", e)
				}
			}
		})
	}
	if e = k.Create(ctx, nc); e != nil {
		t.Fatal(e)
	}
	a.opts.Controller = nc.DeepCopy()
	a.kube = k
	cfg.UID = ""
	cfg.ResourceVersion = ""
	cfg.Generation = 0
	if e = k.Create(ctx, cfg); e != nil {
		t.Fatal(e)
	}
	for i := 0; i < 64; i++ {
		cfg = reconcileFixture(t, a, cfg)
		if i%5 == 0 {
			t.Logf("reconciliation pass %d: %s", i+1, cfg.Status.Phase)
		}
		if cfg.Status.Phase == configv1.NetworkControllerConfigPhaseInSync {
			break
		}
	}
	if cfg.Status.Phase != configv1.NetworkControllerConfigPhaseInSync {
		t.Fatalf("reconciliation: %s %v", cfg.Status.Phase, cfg.Status.Conditions)
	}
	if cfg.Status.LastAppliedTime == nil {
		t.Fatal("missing verified apply time")
	}
	if os.Getenv("CVK_SDWAN_ENVTEST_LIVE") != "1" && api.posts != 1 {
		t.Fatal("duplicate creation")
	}
	t.Log("real Kubernetes status/Lease persistence and idempotent reconciliation passed")
}
