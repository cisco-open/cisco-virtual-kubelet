// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package ise

import (
	"context"
	"testing"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
)

type fakeRESTClient struct {
	gets  []string
	posts []string
	puts  []string
}

func (f *fakeRESTClient) Check(context.Context) error { return nil }
func (f *fakeRESTClient) Close() error                { return nil }
func (f *fakeRESTClient) Get(_ context.Context, path string) ([]byte, error) {
	f.gets = append(f.gets, path)
	return []byte(`{"SearchResult":{"resources":[]}}`), nil
}
func (f *fakeRESTClient) Post(_ context.Context, path string, _ any) ([]byte, error) {
	f.posts = append(f.posts, path)
	return []byte(`{}`), nil
}
func (f *fakeRESTClient) Put(_ context.Context, path string, _ any) ([]byte, error) {
	f.puts = append(f.puts, path)
	return []byte(`{}`), nil
}

func TestDecodeAndCollectISEResources(t *testing.T) {
	cfg, err := DecodeNetAsCodeBody([]byte(`
ise:
  network_resources:
    network_device_groups:
      - name: Location#All Locations#Lab
        description: Lab devices
    network_devices:
      - name: switch-01
        description: access switch
`), "ise-01")
	if err != nil {
		t.Fatalf("DecodeNetAsCodeBody: %v", err)
	}
	resources, err := CollectResources(cfg, []string{"network_resources"})
	if err != nil {
		t.Fatalf("CollectResources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("resources=%d", len(resources))
	}
	if resources[0].Family != "network_resources" || resources[0].Name == "" || resources[0].Endpoint == "" {
		t.Fatalf("unexpected resource: %#v", resources[0])
	}
}

func TestNetAsCodeApplierCreatesMissingResources(t *testing.T) {
	client := &fakeRESTClient{}
	applier := NewNetAsCodeApplierWithClient(client)
	result, err := applier.Apply(context.Background(), Intent{
		DeviceName:      "ise-01",
		ManagedFamilies: []string{"network_resources"},
		DriftPolicy:     configv1alpha1.DriftPolicyRevert,
		Configuration: map[string]any{
			"network_resources": map[string]any{
				"network_device_groups": []any{map[string]any{"name": "Location#All Locations#Lab"}},
			},
		},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !result.Applied || len(client.posts) != 1 || client.posts[0] != "/ers/config/networkdevicegroup" {
		t.Fatalf("unexpected apply result=%#v posts=%v", result, client.posts)
	}
	if len(result.FamilyResults) != 1 || result.FamilyResults[0].State != "InSync" {
		t.Fatalf("family status=%#v", result.FamilyResults)
	}
}
