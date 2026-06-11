// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package sonic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	gpb "github.com/openconfig/gnmi/proto/gnmi"
	v1 "k8s.io/api/core/v1"
)

type fakeGNMIClient struct {
	caps      *gpb.CapabilityResponse
	get       map[string][]byte
	setOps    []OpenConfigOperation
	setCalled bool
}

func (f *fakeGNMIClient) Capabilities(context.Context) (*gpb.CapabilityResponse, error) {
	if f.caps != nil {
		return f.caps, nil
	}
	return &gpb.CapabilityResponse{
		GNMIVersion: "0.7.0",
		SupportedModels: []*gpb.ModelData{
			{Name: "openconfig-interfaces", Version: "2024-05-01"},
			{Name: "openconfig-platform", Version: "2024-05-01"},
		},
	}, nil
}

func (f *fakeGNMIClient) GetJSON(_ context.Context, path string) ([]byte, error) {
	if f.get != nil {
		if b, ok := f.get[path]; ok {
			return b, nil
		}
	}
	return []byte(`{"openconfig-interfaces:interfaces":{"interface":[]}}`), nil
}

func (f *fakeGNMIClient) Set(_ context.Context, ops []OpenConfigOperation) error {
	f.setCalled = true
	f.setOps = append([]OpenConfigOperation(nil), ops...)
	return nil
}

func (f *fakeGNMIClient) Close() error { return nil }

func TestParseGNMIPathStripsModulesAndKeys(t *testing.T) {
	p, err := parseGNMIPath(`/openconfig-interfaces:interfaces/interface[name=Ethernet0]/config/description`)
	if err != nil {
		t.Fatalf("parseGNMIPath: %v", err)
	}
	if len(p.Elem) != 4 {
		t.Fatalf("expected 4 elems, got %d", len(p.Elem))
	}
	if p.Elem[0].Name != "interfaces" || p.Elem[1].Name != "interface" {
		t.Fatalf("unexpected elems: %#v", p.Elem)
	}
	if got := p.Elem[1].Key["name"]; got != "Ethernet0" {
		t.Fatalf("interface key = %q", got)
	}
}

func TestDecodeOpenConfigOperations(t *testing.T) {
	raw := []byte(`openconfig:
  update:
    - path: /interfaces/interface[name=Ethernet0]/config/description
      value: managed-by-cvk
  delete:
    - /interfaces/interface[name=Ethernet4]/config/description
`)
	ops, err := DecodeOpenConfigOperations(raw)
	if err != nil {
		t.Fatalf("DecodeOpenConfigOperations: %v", err)
	}
	if len(ops) != 2 {
		t.Fatalf("expected 2 ops, got %d", len(ops))
	}
	if ops[0].Verb != OperationUpdate || !strings.Contains(ops[0].Path, "Ethernet0") {
		t.Fatalf("unexpected first op: %#v", ops[0])
	}
	var got string
	if err := json.Unmarshal(ops[0].Value, &got); err != nil || got != "managed-by-cvk" {
		t.Fatalf("value = %q err=%v", got, err)
	}
	if ops[1].Verb != OperationDelete || !strings.Contains(ops[1].Path, "Ethernet4") {
		t.Fatalf("unexpected second op: %#v", ops[1])
	}
}

func TestValidateOperationsRejectsOutsideManagedPath(t *testing.T) {
	err := ValidateOperations([]OpenConfigOperation{{Verb: OperationUpdate, Path: "/system/config/hostname", Value: json.RawMessage(`"sonic"`)}}, []string{"/interfaces"})
	if err == nil {
		t.Fatal("expected operation outside managedPaths to fail")
	}
}

func TestJSONValueForPathWrapsScalarForSONICTranslib(t *testing.T) {
	val, err := jsonValueForPath("/interfaces/interface[name=Ethernet0]/config/description", json.RawMessage(`"managed-by-cvk"`))
	if err != nil {
		t.Fatalf("jsonValueForPath: %v", err)
	}
	got := string(val.GetJsonIetfVal())
	if got != `{"description":"managed-by-cvk"}` {
		t.Fatalf("wrapped JSON = %s", got)
	}
}

func TestOpenConfigApplierReportDoesNotWrite(t *testing.T) {
	client := &fakeGNMIClient{}
	applier := NewOpenConfigApplierWithClient(client)
	result, err := applier.Apply(context.Background(), OpenConfigIntent{
		DeviceName:   "sonic-01",
		ManagedPaths: []string{"/interfaces"},
		Operations: []OpenConfigOperation{{
			Verb:  OperationUpdate,
			Path:  "/interfaces/interface[name=Ethernet0]/config/description",
			Value: json.RawMessage(`"managed-by-cvk"`),
		}},
		DriftPolicy: configv1alpha1.DriftPolicyReport,
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if client.setCalled {
		t.Fatal("report policy must not call Set")
	}
	if len(result.Drift) != 1 || result.FamilyResults[0].State != "Drifted" {
		t.Fatalf("unexpected report result: %#v", result)
	}
}

func TestOpenConfigApplierWritesOperations(t *testing.T) {
	client := &fakeGNMIClient{}
	applier := NewOpenConfigApplierWithClient(client)
	result, err := applier.Apply(context.Background(), OpenConfigIntent{
		DeviceName:   "sonic-01",
		ManagedPaths: []string{"/interfaces"},
		Operations: []OpenConfigOperation{{
			Verb:  OperationUpdate,
			Path:  "/interfaces/interface[name=Ethernet0]/config/description",
			Value: json.RawMessage(`"managed-by-cvk"`),
		}},
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !client.setCalled || len(client.setOps) != 1 {
		t.Fatalf("expected one Set op, got called=%v ops=%d", client.setCalled, len(client.setOps))
	}
	if !result.Applied || result.FamilyResults[0].State != "InSync" {
		t.Fatalf("unexpected apply result: %#v", result)
	}
}

func TestSONICDriverHealthResourcesAndUnsupportedAppHosting(t *testing.T) {
	driver := &SONICDriver{
		config: &v1alpha1.DeviceSpec{SONIC: &v1alpha1.SONICConfig{Resources: &v1alpha1.SONICResourceConfig{CPUCores: 8, MemoryMB: 32768}}},
		gnmi:   &fakeGNMIClient{},
	}
	if err := driver.CheckConnection(context.Background()); err != nil {
		t.Fatalf("CheckConnection: %v", err)
	}
	info, err := driver.GetDeviceInfo(context.Background())
	if err != nil {
		t.Fatalf("GetDeviceInfo: %v", err)
	}
	if info.SoftwareVersion != "0.7.0" || info.ProductID != "Cisco SONiC" {
		t.Fatalf("unexpected device info: %#v", info)
	}
	res, err := driver.GetDeviceResources(context.Background())
	if err != nil {
		t.Fatalf("GetDeviceResources: %v", err)
	}
	podCapacity := (*res)[v1.ResourcePods]
	if pods := podCapacity.Value(); pods != 0 {
		t.Fatalf("pods capacity = %d", pods)
	}
	oper, err := driver.GetGlobalOperationalData(context.Background())
	if err != nil {
		t.Fatalf("GetGlobalOperationalData: %v", err)
	}
	if !oper.AppHostingUnsupported || oper.SystemCPU.Quota != 8 {
		t.Fatalf("unexpected oper data: %#v", oper)
	}
}

func TestDecodeOpenConfigInterfaceStats(t *testing.T) {
	raw := []byte(`{
  "openconfig-interfaces:interfaces": {
    "interface": [
      {
        "name": "Ethernet0",
        "state": {
          "oper-status": "UP",
          "speed": "SPEED_100GB",
          "counters": {"in-octets": "101", "out-octets": "202"}
        }
      }
    ]
  }
}`)
	driver := &SONICDriver{gnmi: &fakeGNMIClient{get: map[string][]byte{"/interfaces": raw}}}
	stats, err := driver.GetInterfaceStats(context.Background())
	if err != nil {
		t.Fatalf("GetInterfaceStats: %v", err)
	}
	if len(stats) != 1 {
		t.Fatalf("expected one interface, got %d", len(stats))
	}
	if stats[0].Name != "Ethernet0" || stats[0].OperStatus != "up" || stats[0].InOctets != 101 || stats[0].OutOctets != 202 || stats[0].Speed != 100000000000 {
		t.Fatalf("unexpected stats: %#v", stats[0])
	}
}
