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
	return transport.Capabilities{Kind: transport.KindNXAPI, SupportsWritableRunning: true, SupportsSaveStartup: true}
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
		for _, line := range strings.Split(string(op.Body), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "hostname ") {
				f.hostname = strings.TrimSpace(strings.TrimPrefix(line, "hostname "))
			}
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
