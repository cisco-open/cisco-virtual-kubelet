// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package aci

import (
	"context"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type fakeHealthClient struct{}

func (fakeHealthClient) Check(context.Context) error { return nil }
func (fakeHealthClient) Close() error                { return nil }
func (fakeHealthClient) Info(context.Context) (*APICInfo, error) {
	return &APICInfo{
		Login: LoginInfo{Version: "6.2(2e)", UserName: "admin"},
		System: TopSystem{
			Name:    "apic1",
			Serial:  "TEP-1-1",
			Version: "6.2(2e)",
			Role:    "controller",
			State:   "in-service",
		},
		Nodes: []FabricNode{{ID: "1", Name: "apic1", Role: "controller", Version: "6.2(2e)", FabricSt: "commissioned"}},
	}, nil
}
func (fakeHealthClient) FabricNodes(context.Context) ([]FabricNode, error) {
	return []FabricNode{{ID: "1", Name: "apic1", Role: "controller", Version: "6.2(2e)", FabricSt: "commissioned"}}, nil
}
func (fakeHealthClient) TopSystem(context.Context) (*TopSystem, error) {
	return &TopSystem{Name: "apic1", Version: "6.2(2e)", Serial: "TEP-1-1", State: "in-service"}, nil
}

func TestACIDriverAdvertisesHealthNodeWithoutPods(t *testing.T) {
	driver := &ACIDriver{
		config: &v1alpha1.DeviceSpec{APIC: &v1alpha1.APICConfig{Resources: &v1alpha1.APICResourceConfig{CPUCores: 8, MemoryMB: 49152, StorageMB: 600000}}},
		client: fakeHealthClient{},
	}
	resources, err := driver.GetDeviceResources(context.Background())
	if err != nil {
		t.Fatalf("GetDeviceResources: %v", err)
	}
	if got := resources.Pods().Value(); got != 0 {
		t.Fatalf("pods=%d", got)
	}
	oper, err := driver.GetGlobalOperationalData(context.Background())
	if err != nil {
		t.Fatalf("GetGlobalOperationalData: %v", err)
	}
	if !oper.AppHostingUnsupported || oper.IoxEnabled {
		t.Fatalf("unexpected app-hosting state: %#v", oper)
	}
	if oper.SystemCPU.Quota != 8 || oper.Memory.Quota != 49152 || oper.Storage.Quota != 600000 {
		t.Fatalf("unexpected resources: %#v", oper)
	}
	if !strings.Contains(oper.AppHostingMessage, "fabric nodes 1") {
		t.Fatalf("message=%q", oper.AppHostingMessage)
	}
}

func TestACIDriverRejectsAppHosting(t *testing.T) {
	driver := &ACIDriver{}
	err := driver.DeployPod(context.Background(), &v1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "hello"}}, nil, nil)
	if !apierrors.IsForbidden(err) {
		t.Fatalf("DeployPod err=%v, want forbidden", err)
	}
	if !strings.Contains(err.Error(), ErrAppHostingUnsupported.Error()) {
		t.Fatalf("DeployPod err=%v, want ErrAppHostingUnsupported", err)
	}
	pods, err := driver.ListPods(context.Background())
	if err != nil || len(pods) != 0 {
		t.Fatalf("ListPods pods=%v err=%v", pods, err)
	}
}

func TestACIOperationalCommands(t *testing.T) {
	driver := &ACIDriver{client: fakeHealthClient{}}
	out, err := driver.RunOperationalCommand(context.Background(), "show nodes")
	if err != nil {
		t.Fatalf("RunOperationalCommand: %v", err)
	}
	if !strings.Contains(out, "apic1") || !strings.Contains(out, "commissioned") {
		t.Fatalf("output=%q", out)
	}
}
