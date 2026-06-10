// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package ise

import (
	"context"
	"strings"
	"testing"

	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	v1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const sampleISEShowVersion = `
Cisco Identity Services Engine
Version      : 3.5.0.527
Hostname     : ise-01
Serial Number: ISEVM12345
`

const sampleISEApplicationStatus = `
ISE PROCESS NAME                       STATE            PROCESS ID
------------------------------------------------------------------
Application Server                     running          1234
API Gateway Database Service           running          1235
ISE Indexing Engine                    running          1236
`

type fakeISECommandClient struct {
	outputs map[string]string
}

func (f fakeISECommandClient) Run(_ context.Context, command string) (string, error) {
	return f.outputs[command], nil
}

func TestParseShowVersion(t *testing.T) {
	info := parseShowVersion(sampleISEShowVersion)
	if info.ProductID != "Cisco Identity Services Engine" {
		t.Fatalf("product=%q", info.ProductID)
	}
	if info.SoftwareVersion != "3.5.0.527" {
		t.Fatalf("version=%q", info.SoftwareVersion)
	}
	if info.Hostname != "ise-01" {
		t.Fatalf("hostname=%q", info.Hostname)
	}
	if info.SerialNumber != "ISEVM12345" {
		t.Fatalf("serial=%q", info.SerialNumber)
	}
}

func TestISEDriverAdvertisesHealthNodeWithoutPods(t *testing.T) {
	driver := &ISEDriver{
		config: &v1alpha1.DeviceSpec{ISE: &v1alpha1.ISEConfig{Resources: &v1alpha1.ISEResourceConfig{CPUCores: 20, MemoryMB: 49152, StorageMB: 600000}}},
		client: fakeISECommandClient{outputs: map[string]string{"show application status ise": sampleISEApplicationStatus}},
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
	if oper.SystemCPU.Quota != 20 || oper.Memory.Quota != 49152 || oper.Storage.Quota != 600000 {
		t.Fatalf("unexpected resources: %#v", oper)
	}
}

func TestISEDriverRejectsAppHosting(t *testing.T) {
	driver := &ISEDriver{}
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
