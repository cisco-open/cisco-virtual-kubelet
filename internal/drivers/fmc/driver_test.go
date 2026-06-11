// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package fmc

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
func (fakeHealthClient) ServerVersion(context.Context) (*ServerVersion, error) {
	return &ServerVersion{Hostname: "fmc-01", ServerVersion: "7.6.5 (build 106)", Model: "Cisco Secure Firewall Management Center for KVM", UUID: "uuid-1"}, nil
}
func (fakeHealthClient) SmartLicense(context.Context) (*SmartLicense, error) {
	return &SmartLicense{RegStatus: "EVALUATION", Metadata: map[string]any{"evalExpiresInDays": 89}}, nil
}
func (fakeHealthClient) ManagedDevices(context.Context) ([]ManagedDevice, error) {
	return []ManagedDevice{{Name: "ftdv-01", HostName: "192.0.2.65", HealthStatus: "green", DeploymentStatus: "DEPLOYED"}}, nil
}

func TestFMCDriverAdvertisesHealthNodeWithoutPods(t *testing.T) {
	driver := &FMCDriver{
		config: &v1alpha1.DeviceSpec{FMC: &v1alpha1.FMCConfig{Resources: &v1alpha1.FMCResourceConfig{CPUCores: 8, MemoryMB: 49152, StorageMB: 600000}}},
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
	if !strings.Contains(oper.AppHostingMessage, "managing 1 device") {
		t.Fatalf("message=%q", oper.AppHostingMessage)
	}
}

func TestFMCDriverRejectsAppHosting(t *testing.T) {
	driver := &FMCDriver{}
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

func TestFMCOperationalCommands(t *testing.T) {
	driver := &FMCDriver{client: fakeHealthClient{}}
	out, err := driver.RunOperationalCommand(context.Background(), "show devices")
	if err != nil {
		t.Fatalf("RunOperationalCommand: %v", err)
	}
	if !strings.Contains(out, "ftdv-01") {
		t.Fatalf("output=%q", out)
	}
}
