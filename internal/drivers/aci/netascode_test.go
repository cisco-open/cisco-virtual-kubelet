// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package aci

import (
	"context"
	"testing"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

type fakeRESTClient struct {
	posts []moIntent
}

func (f *fakeRESTClient) Check(context.Context) error { return nil }
func (f *fakeRESTClient) Close() error                { return nil }
func (f *fakeRESTClient) PostMO(_ context.Context, dn, class string, attrs map[string]string, children []any) error {
	f.posts = append(f.posts, moIntent{DN: dn, Class: class, Attrs: attrs, Children: children})
	return nil
}

func TestDecodeAndCollectAPICResources(t *testing.T) {
	cfg, err := DecodeNetAsCodeBody([]byte(`
existing:
  apic:
    tenants:
      - name: common
apic:
  tenants:
    - name: cvk_probe
      description: CVK tenant
      vrfs:
        - name: cvk_vrf
      bridge_domains:
        - name: cvk_bd
          vrf: cvk_vrf
          subnets:
            - ip: 198.51.101.1/24
  access_policies:
    vlan_pools:
      - name: CVK_POOL
        ranges:
          - from: 100
            to: 199
`), "apic-01")
	if err != nil {
		t.Fatalf("DecodeNetAsCodeBody: %v", err)
	}
	if _, ok := cfg["existing"].(map[string]any); !ok {
		t.Fatalf("existing.apic was not preserved: %#v", cfg)
	}
	resources, err := CollectManagedObjects(cfg, []string{"tenants", "access_policies"})
	if err != nil {
		t.Fatalf("CollectManagedObjects: %v", err)
	}
	wantDNs := map[string]bool{
		"uni/infra/vlanns-[CVK_POOL]-static":                               false,
		"uni/infra/vlanns-[CVK_POOL]-static/from-[vlan-100]-to-[vlan-199]": false,
		"uni/tn-cvk_probe":                                    false,
		"uni/tn-cvk_probe/ctx-cvk_vrf":                        false,
		"uni/tn-cvk_probe/BD-cvk_bd":                          false,
		"uni/tn-cvk_probe/BD-cvk_bd/subnet-[198.51.101.1/24]": false,
	}
	for _, res := range resources {
		if _, ok := wantDNs[res.DN]; ok {
			wantDNs[res.DN] = true
		}
	}
	for dn, seen := range wantDNs {
		if !seen {
			t.Fatalf("missing DN %s in %#v", dn, resources)
		}
	}
}

func TestNetAsCodeApplierCreatesManagedObjects(t *testing.T) {
	client := &fakeRESTClient{}
	applier := NewNetAsCodeApplierWithClient(client)
	result, err := applier.Apply(context.Background(), Intent{
		DeviceName:      "apic-01",
		ManagedFamilies: []string{"tenants"},
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
		Configuration: map[string]any{
			"tenants": []any{map[string]any{
				"name": "cvk_probe",
				"vrfs": []any{map[string]any{"name": "cvk_vrf"}},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied || len(client.posts) != 2 {
		t.Fatalf("unexpected apply result=%#v posts=%v", result, client.posts)
	}
	if client.posts[0].DN != "uni/tn-cvk_probe" || client.posts[1].DN != "uni/tn-cvk_probe/ctx-cvk_vrf" {
		t.Fatalf("posts=%#v", client.posts)
	}
	if len(result.FamilyResults) != 1 || result.FamilyResults[0].State != "InSync" {
		t.Fatalf("family status=%#v", result.FamilyResults)
	}
}

func TestNetAsCodeApplierReportModeDoesNotWrite(t *testing.T) {
	client := &fakeRESTClient{}
	applier := NewNetAsCodeApplierWithClient(client)
	result, err := applier.Apply(context.Background(), Intent{
		DeviceName:      "apic-01",
		ManagedFamilies: []string{"tenants"},
		DriftPolicy:     configv1alpha1.DriftPolicyReport,
		Configuration:   map[string]any{"tenants": []any{map[string]any{"name": "cvk_probe"}}},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(client.posts) != 0 {
		t.Fatalf("report mode wrote posts=%#v", client.posts)
	}
	if len(result.Drift) != 1 || result.FamilyResults[0].State != "Drifted" {
		t.Fatalf("unexpected report result=%#v", result)
	}
}
