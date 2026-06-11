// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package fmc

import (
	"context"
	"testing"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

type fakeRESTClient struct {
	gets  []string
	posts []string
	puts  []string
	items map[string][]map[string]any
}

func (f *fakeRESTClient) Check(context.Context) error { return nil }
func (f *fakeRESTClient) Close() error                { return nil }
func (f *fakeRESTClient) DomainUUIDForName(context.Context, string) (string, error) {
	return "domain-uuid", nil
}
func (f *fakeRESTClient) Get(_ context.Context, path string) ([]byte, error) {
	f.gets = append(f.gets, path)
	return []byte(`{"items":[]}`), nil
}
func (f *fakeRESTClient) ListItems(_ context.Context, path string) ([]map[string]any, error) {
	f.gets = append(f.gets, path)
	if f.items != nil {
		return f.items[path], nil
	}
	return nil, nil
}
func (f *fakeRESTClient) Post(_ context.Context, path string, _ any) ([]byte, error) {
	f.posts = append(f.posts, path)
	return []byte(`{}`), nil
}
func (f *fakeRESTClient) Put(_ context.Context, path string, _ any) ([]byte, error) {
	f.puts = append(f.puts, path)
	return []byte(`{}`), nil
}

func TestDecodeAndCollectFMCResources(t *testing.T) {
	cfg, err := DecodeNetAsCodeBody([]byte(`
existing:
  fmc:
    domains:
      - name: Global
        objects:
          networks:
            - name: any-ipv4
fmc:
  domains:
    - name: Global
      objects:
        hosts:
          - name: cvk-host
            ip: 198.51.100.10
        networks:
          - name: cvk-net
            prefix: 198.51.100.0/24
`), "fmc-01")
	if err != nil {
		t.Fatalf("DecodeNetAsCodeBody: %v", err)
	}
	if _, ok := cfg["existing"].(map[string]any); !ok {
		t.Fatalf("existing.fmc was not preserved: %#v", cfg)
	}
	resources, err := CollectResources(cfg, []string{"objects"})
	if err != nil {
		t.Fatalf("CollectResources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("resources=%d", len(resources))
	}
	if resources[0].Family != "objects" || resources[0].Name == "" || resources[0].Def.Endpoint == "" {
		t.Fatalf("unexpected resource: %#v", resources[0])
	}
}

func TestNetAsCodeApplierCreatesMissingResources(t *testing.T) {
	client := &fakeRESTClient{}
	applier := NewNetAsCodeApplierWithClient(client)
	result, err := applier.Apply(context.Background(), Intent{
		DeviceName:      "fmc-01",
		ManagedFamilies: []string{"objects"},
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
		Configuration: map[string]any{
			"domains": []any{map[string]any{
				"name": "Global",
				"objects": map[string]any{
					"hosts": []any{map[string]any{"name": "cvk-host", "ip": "198.51.100.10"}},
				},
			}},
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := "/api/fmc_config/v1/domain/domain-uuid/object/hosts"
	if !result.Applied || len(client.posts) != 1 || client.posts[0] != want {
		t.Fatalf("unexpected apply result=%#v posts=%v", result, client.posts)
	}
	if len(result.FamilyResults) != 1 || result.FamilyResults[0].State != "InSync" {
		t.Fatalf("family status=%#v", result.FamilyResults)
	}
}

func TestNetAsCodeApplierUpdatesExistingResources(t *testing.T) {
	endpoint := "/api/fmc_config/v1/domain/domain-uuid/object/hosts"
	client := &fakeRESTClient{items: map[string][]map[string]any{endpoint: {{"id": "abc", "name": "cvk-host"}}}}
	applier := NewNetAsCodeApplierWithClient(client)
	_, err := applier.Apply(context.Background(), Intent{
		DeviceName:      "fmc-01",
		ManagedFamilies: []string{"objects"},
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
		Configuration:   map[string]any{"domains": []any{map[string]any{"name": "Global", "objects": map[string]any{"hosts": []any{map[string]any{"name": "cvk-host", "ip": "198.51.100.10"}}}}}},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(client.puts) != 1 || client.puts[0] != endpoint+"/abc" {
		t.Fatalf("puts=%v", client.puts)
	}
}
