// Copyright © 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package provider

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/transport"
	nxosschema "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/schema"
	nxoswriters "github.com/cisco/virtual-kubelet-cisco/internal/drivers/nxos/configdriver/writers"
)

type fakeNXOSTransport struct {
	hostname string
	fetches  int
	saves    int
}

func (f *fakeNXOSTransport) Capabilities() transport.Capabilities {
	return transport.Capabilities{Kind: transport.KindREST, SupportsWritableRunning: true, SupportsSaveStartup: true}
}

func (f *fakeNXOSTransport) Fetch(_ context.Context, path string) ([]byte, error) {
	f.fetches++
	switch path {
	case nxosschema.PathSystemHostname:
		return json.Marshal(map[string]any{"hostname": f.hostname})
	case nxosschema.PathVLANBrief:
		return json.Marshal(map[string]any{"vlans": []any{}})
	case nxosschema.PathInterfaceEthernet:
		return json.Marshal(map[string]any{"interfaces": []any{}})
	default:
		return nil, transport.ErrUnsupported
	}
}

func (f *fakeNXOSTransport) StartTransaction(context.Context) (transport.TxHandle, error) {
	return "", transport.ErrUnsupported
}

func (f *fakeNXOSTransport) Mutate(_ context.Context, _ transport.TxHandle, ops []transport.Op) error {
	for _, op := range ops {
		var body map[string]any
		if err := json.Unmarshal(op.Body, &body); err != nil {
			return err
		}
		top, _ := body["topSystem"].(map[string]any)
		attrs, _ := top["attributes"].(map[string]any)
		if name, ok := attrs["name"].(string); ok {
			f.hostname = name
		}
	}
	return nil
}

func (*fakeNXOSTransport) Commit(context.Context, transport.TxHandle) error  { return nil }
func (*fakeNXOSTransport) Discard(context.Context, transport.TxHandle) error { return nil }
func (f *fakeNXOSTransport) SaveStartup(context.Context) error {
	f.saves++
	return nil
}
func (*fakeNXOSTransport) Close() error { return nil }

func TestNXOSConfigReconcilerRecordsInSync(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     &configv1alpha1.NetAsCodeModelSource{Format: configv1alpha1.NetAsCodeModelFormatNXOS, Resolved: true},
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     nxoswriters.GetForRelease,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.hostname != "leaf-01" {
		t.Fatalf("hostname=%q", tr.hostname)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.Phase != "InSync" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
	if len(got.Status.FamilyStatus) != 1 || got.Status.FamilyStatus[0].Name != "system" {
		t.Fatalf("family status=%#v", got.Status.FamilyStatus)
	}
}

func TestNXOSConfigReconcilerRecordsInSyncFromFullNetAsCodeEnvelope(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{
		"nxos": {
			"global": {
				"variables": {"hostname": "leaf-01"},
				"configuration": {"system": {"hostname": "${hostname}"}}
			},
			"devices": [{"name": "leaf-01"}]
		}
	}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     &configv1alpha1.NetAsCodeModelSource{Format: configv1alpha1.NetAsCodeModelFormatNXOS, Resolved: true},
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     nxoswriters.GetForRelease,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.hostname != "leaf-01" {
		t.Fatalf("hostname=%q", tr.hostname)
	}
}

func TestNXOSConfigReconcilerReturnsResolveErrorAfterRecordingFailure(t *testing.T) {
	scheme := newTestScheme(t)
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     &configv1alpha1.NetAsCodeModelSource{Format: configv1alpha1.NetAsCodeModelFormatNXOS, Resolved: true},
			Source: configv1alpha1.ConfigurationSource{
				ConfigMapRef: &configv1alpha1.ConfigMapKeyRef{Name: "missing", Key: "config.yaml"},
			},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err == nil {
		t.Fatal("Reconcile error=nil, want resolve error returned to controller-runtime")
	}
	if !strings.Contains(err.Error(), "get ConfigMap network/missing") {
		t.Fatalf("Reconcile error=%q, want missing ConfigMap context", err)
	}
	var got configv1alpha1.NXOSConfig
	if getErr := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); getErr != nil {
		t.Fatalf("get updated: %v", getErr)
	}
	if got.Status.Phase != "Failed" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
	if len(got.Status.Conditions) == 0 || got.Status.Conditions[0].Reason != "ReconcileFailed" {
		t.Fatalf("conditions=%#v", got.Status.Conditions)
	}
}

func TestNXOSConfigReconcilerRuntimeOptions(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     &configv1alpha1.NetAsCodeModelSource{Format: configv1alpha1.NetAsCodeModelFormatNXOS, Resolved: true},
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
			WriteStartup:    true,
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "old"}
	r := &NXOSConfigReconciler{
		Client:        c,
		DeviceName:    "leaf-01",
		Transport:     tr,
		Lookup:        nxoswriters.GetForRelease,
		DeviceVersion: "10.3(9)",
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	if tr.saves != 1 {
		t.Fatalf("SaveStartup calls=%d", tr.saves)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.SourceYangVersion != "10.3(9)" {
		t.Fatalf("sourceYangVersion=%q", got.Status.SourceYangVersion)
	}
	if got.Status.PlannedOps == 0 || got.Status.AppliedOps == 0 {
		t.Fatalf("planned/applied ops not recorded: planned=%d applied=%d", got.Status.PlannedOps, got.Status.AppliedOps)
	}
	if len(got.Status.VerifiedFamilies) != 1 || got.Status.VerifiedFamilies[0] != "system" {
		t.Fatalf("verifiedFamilies=%#v", got.Status.VerifiedFamilies)
	}
	if got.Status.PostApplyObservedHash == "" {
		t.Fatal("postApplyObservedHash was not recorded")
	}
}

func TestNXOSConfigReconcilerRecordsConfirmedCommitFallback(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:             configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies:       []string{"system"},
			ModelSource:           &configv1alpha1.NetAsCodeModelSource{Format: configv1alpha1.NetAsCodeModelFormatNXOS, Resolved: true},
			Source:                configv1alpha1.ConfigurationSource{Inline: &raw},
			Transactional:         true,
			ConfirmTimeoutSeconds: 60,
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  &fakeNXOSTransport{hostname: "old"},
		Lookup:     nxoswriters.GetForRelease,
	}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.NXOSConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "network", Name: "leaf-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.Phase != "InSync" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
	if len(got.Status.TransportFallbacks) != 1 {
		t.Fatalf("transportFallbacks=%#v, want confirmed-commit fallback", got.Status.TransportFallbacks)
	}
	fallback := got.Status.TransportFallbacks[0]
	if fallback.Type != "ConfirmedCommit" || fallback.Reason != "non-transactional reconcile" {
		t.Fatalf("fallback=%#v", fallback)
	}
}

func TestNXOSConfigReconcilerSubscribeBypassesHashShortCircuit(t *testing.T) {
	scheme := newTestScheme(t)
	raw := runtime.RawExtension{Raw: []byte(`{"system":{"hostname":"leaf-01"}}`)}
	cr := &configv1alpha1.NXOSConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "leaf-config", Namespace: "network", Generation: 1},
		Spec: configv1alpha1.NXOSConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "leaf-01"},
			ManagedFamilies: []string{"system"},
			ModelSource:     &configv1alpha1.NetAsCodeModelSource{Format: configv1alpha1.NetAsCodeModelFormatNXOS, Resolved: true},
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithStatusSubresource(&configv1alpha1.NXOSConfig{}).
		WithObjects(cr).
		Build()
	tr := &fakeNXOSTransport{hostname: "leaf-01"}
	r := &NXOSConfigReconciler{
		Client:     c,
		DeviceName: "leaf-01",
		Transport:  tr,
		Lookup:     nxoswriters.GetForRelease,
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "network", Name: "leaf-config"}}
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("initial Reconcile: %v", err)
	}
	if tr.fetches == 0 {
		t.Fatal("initial reconcile did not touch transport")
	}

	tr.fetches = 0
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("short-circuit Reconcile: %v", err)
	}
	if tr.fetches != 0 {
		t.Fatalf("normal event should have short-circuited before Fetch, fetches=%d", tr.fetches)
	}

	time.Sleep(2 * time.Millisecond)
	r.NotifySubscribeFired()
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("subscribe Reconcile: %v", err)
	}
	if tr.fetches == 0 {
		t.Fatal("subscribe event should bypass hash short-circuit and Fetch")
	}
}

func TestNormalizeNXOSNetAsCodeSourceEthernets(t *testing.T) {
	got, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"system": map[string]any{"hostname": "leaf-01"},
		"interfaces": map[string]any{
			"ethernets": []any{
				map[string]any{"id": "1/49", "description": "uplink", "shutdown": false},
			},
		},
	}, "leaf-01")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if _, ok := got["interfaces"]; ok {
		t.Fatalf("interfaces should be consumed when only ethernets are present: %#v", got)
	}
	intfFamily, ok := got["interface_ethernet"].(map[string]any)
	if !ok {
		t.Fatalf("interface_ethernet missing: %#v", got)
	}
	list, ok := intfFamily["interfaces"].([]any)
	if !ok || len(list) != 1 {
		t.Fatalf("interfaces list=%#v", intfFamily["interfaces"])
	}
	item, ok := list[0].(map[string]any)
	if !ok {
		t.Fatalf("item=%#v", list[0])
	}
	want := map[string]any{
		"id":          "1/49",
		"name":        "1/49",
		"type":        "Ethernet",
		"description": "uplink",
		"shutdown":    false,
	}
	if !reflect.DeepEqual(item, want) {
		t.Fatalf("item=%#v, want %#v", item, want)
	}
}

func TestNormalizeNXOSNetAsCodeSourceExtractsDeviceConfiguration(t *testing.T) {
	got, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"devices": []any{
			map[string]any{
				"name":          "other",
				"configuration": map[string]any{"system": map[string]any{"hostname": "other"}},
			},
			map[string]any{
				"name":          "leaf-01",
				"configuration": map[string]any{"system": map[string]any{"hostname": "leaf-01"}},
			},
		},
	}, "leaf-01")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	system, ok := got["system"].(map[string]any)
	if !ok || system["hostname"] != "leaf-01" {
		t.Fatalf("system=%#v", got["system"])
	}
}

func TestNormalizeNXOSNetAsCodeSourceResolvesScopedEnvelope(t *testing.T) {
	got, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"nxos": map[string]any{
			"templates": []any{
				map[string]any{
					"name":  "BASE_VLAN",
					"order": 10,
					"configuration": map[string]any{
						"vlan": map[string]any{"vlans": []any{
							map[string]any{"id": 20, "name": "${access_vlan_name}"},
						}},
					},
				},
				map[string]any{
					"name": "SAME_A",
					"configuration": map[string]any{
						"vlan": map[string]any{"vlans": []any{
							map[string]any{"id": 30, "name": "A"},
						}},
					},
				},
				map[string]any{
					"name": "SAME_B",
					"configuration": map[string]any{
						"vlan": map[string]any{"vlans": []any{
							map[string]any{"id": 30, "name": "B"},
						}},
					},
				},
			},
			"interface_groups": []any{
				map[string]any{
					"name": "EDGE",
					"configuration": map[string]any{
						"description": "group ${hostname}",
						"shutdown":    false,
						"mtu":         9216,
					},
				},
			},
			"global": map[string]any{
				"variables": map[string]any{
					"hostname":         "global",
					"access_vlan_name": "global-access",
				},
				"templates": []any{"BASE_VLAN"},
				"configuration": map[string]any{
					"system": map[string]any{"hostname": "${hostname}"},
					"vlan": map[string]any{"vlans": []any{
						map[string]any{"id": 10, "name": "GLOBAL"},
					}},
				},
			},
			"device_groups": []any{
				map[string]any{
					"name":    "LEAFS",
					"devices": []any{"leaf-01"},
					"variables": map[string]any{
						"hostname":         "group",
						"access_vlan_name": "group-access",
					},
					"configuration": map[string]any{
						"vlan": map[string]any{"vlans": []any{
							map[string]any{"id": 10, "name": "GROUP"},
						}},
						"interfaces": map[string]any{"ethernets": []any{
							map[string]any{"id": "1/2", "interface_groups": []any{"EDGE"}},
						}},
					},
				},
			},
			"devices": []any{
				map[string]any{
					"name":          "leaf-01",
					"device_groups": []any{"LEAFS"},
					"variables":     map[string]any{"hostname": "leaf-01"},
					"templates":     []any{"SAME_A", "SAME_B"},
					"configuration": map[string]any{
						"vlan": map[string]any{"vlans": []any{
							map[string]any{"id": 10, "name": "DEVICE"},
						}},
						"interfaces": map[string]any{"ethernets": []any{
							map[string]any{"id": "1/1", "interface_groups": []any{"EDGE"}, "description": "local"},
						}},
					},
				},
			},
		},
	}, "leaf-01")
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	system, ok := got["system"].(map[string]any)
	if !ok || system["hostname"] != "leaf-01" {
		t.Fatalf("system=%#v", got["system"])
	}
	vlan, ok := got["vlan"].(map[string]any)
	if !ok {
		t.Fatalf("vlan missing: %#v", got)
	}
	vlans, ok := vlan["vlans"].([]any)
	if !ok || len(vlans) != 3 {
		t.Fatalf("vlans=%#v", vlan["vlans"])
	}
	if item := nxosTestMapByKey(t, vlans, "id", 10); item["name"] != "DEVICE" {
		t.Fatalf("vlan 10=%#v", item)
	}
	if item := nxosTestMapByKey(t, vlans, "id", 20); item["name"] != "group-access" {
		t.Fatalf("vlan 20=%#v", item)
	}
	if item := nxosTestMapByKey(t, vlans, "id", 30); item["name"] != "B" {
		t.Fatalf("same-order template merge did not preserve reference order: %#v", item)
	}
	if _, ok := got["interfaces"]; ok {
		t.Fatalf("interfaces should be consumed after Ethernet normalization: %#v", got["interfaces"])
	}
	intfFamily, ok := got["interface_ethernet"].(map[string]any)
	if !ok {
		t.Fatalf("interface_ethernet missing: %#v", got)
	}
	intfs, ok := intfFamily["interfaces"].([]any)
	if !ok || len(intfs) != 2 {
		t.Fatalf("interfaces=%#v", intfFamily["interfaces"])
	}
	groupOnly := nxosTestMapByKey(t, intfs, "name", "1/2")
	if groupOnly["description"] != "group leaf-01" || groupOnly["shutdown"] != false || groupOnly["mtu"] != 9216 {
		t.Fatalf("group-expanded interface=%#v", groupOnly)
	}
	deviceOverride := nxosTestMapByKey(t, intfs, "name", "1/1")
	if deviceOverride["description"] != "local" || deviceOverride["shutdown"] != false || deviceOverride["mtu"] != 9216 {
		t.Fatalf("device override interface=%#v", deviceOverride)
	}
}

func TestNormalizeNXOSNetAsCodeSourceRejectsUnsupportedTemplateType(t *testing.T) {
	_, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"nxos": map[string]any{
			"templates": []any{
				map[string]any{
					"name":          "CLI_TEMPLATE",
					"type":          "cli",
					"configuration": map[string]any{"system": map[string]any{"hostname": "leaf-01"}},
				},
			},
			"devices": []any{map[string]any{"name": "leaf-01", "templates": []any{"CLI_TEMPLATE"}}},
		},
	}, "leaf-01")
	if err == nil || !strings.Contains(err.Error(), `template "CLI_TEMPLATE" type "cli" is not supported`) {
		t.Fatalf("err=%v", err)
	}
}

func TestNormalizeNXOSNetAsCodeSourceRejectsUnresolvedVariable(t *testing.T) {
	_, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"nxos": map[string]any{
			"global": map[string]any{
				"configuration": map[string]any{"system": map[string]any{"hostname": "${hostname}"}},
			},
			"devices": []any{map[string]any{"name": "leaf-01"}},
		},
	}, "leaf-01")
	if err == nil || !strings.Contains(err.Error(), `unresolved variable "hostname"`) {
		t.Fatalf("err=%v", err)
	}
}

func TestNormalizeNXOSNetAsCodeSourceRejectsAmbiguousEthernetShapes(t *testing.T) {
	_, err := normalizeNXOSNetAsCodeSource(map[string]any{
		"interface_ethernet": map[string]any{"interfaces": []any{}},
		"interfaces":         map[string]any{"ethernets": []any{}},
	}, "leaf-01")
	if err == nil || !strings.Contains(err.Error(), "both interface_ethernet and interfaces.ethernets") {
		t.Fatalf("err=%v", err)
	}
}

func nxosTestMapByKey(t *testing.T, list []any, key string, value any) map[string]any {
	t.Helper()
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if reflect.DeepEqual(m[key], value) {
			return m
		}
	}
	t.Fatalf("no map with %s=%#v in %#v", key, value, list)
	return nil
}
