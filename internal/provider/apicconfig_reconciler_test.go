// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package provider

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	ciskov1 "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/aci"
)

type fakeAPICConfigApplier struct {
	seen aci.Intent
}

func (*fakeAPICConfigApplier) Health(context.Context) error { return nil }
func (f *fakeAPICConfigApplier) Apply(_ context.Context, intent aci.Intent) (aci.ApplyResult, error) {
	f.seen = intent
	return aci.ApplyResult{FamilyResults: []aci.FamilyResult{{Name: "tenants", State: "InSync", Entries: 1}}}, nil
}
func (*fakeAPICConfigApplier) Close() error { return nil }

func TestAPICConfigReconcilerRecordsInSync(t *testing.T) {
	scheme := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(configv1alpha1.AddToScheme(scheme))
	utilruntime.Must(ciskov1.AddToScheme(scheme))
	raw := runtime.RawExtension{Raw: []byte(`{"tenants":[{"name":"cvk_probe"}]}`)}
	cr := &configv1alpha1.APICConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "apic-01-config", Namespace: "default"},
		Spec: configv1alpha1.APICConfigSpec{
			DeviceRef:       configv1alpha1.DeviceRef{Name: "apic-01"},
			ManagedFamilies: []string{"tenants"},
			ModelSource:     &configv1alpha1.NetAsCodeModelSource{Format: configv1alpha1.NetAsCodeModelFormatAPIC, Resolved: true},
			Source:          configv1alpha1.ConfigurationSource{Inline: &raw},
		},
	}
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(&configv1alpha1.APICConfig{}).WithObjects(cr).Build()
	applier := &fakeAPICConfigApplier{}
	r := &APICConfigReconciler{Client: c, DeviceName: "apic-01", Applier: applier}
	_, err := r.Reconcile(context.Background(), ctrl.Request{NamespacedName: types.NamespacedName{Namespace: "default", Name: "apic-01-config"}})
	if err != nil {
		t.Fatalf("Reconcile: %v", err)
	}
	var got configv1alpha1.APICConfig
	if err := c.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "apic-01-config"}, &got); err != nil {
		t.Fatalf("get updated: %v", err)
	}
	if got.Status.Phase != "InSync" {
		t.Fatalf("phase=%q status=%#v", got.Status.Phase, got.Status)
	}
	if len(got.Status.FamilyStatus) != 1 || got.Status.FamilyStatus[0].Name != "tenants" {
		t.Fatalf("family status=%#v", got.Status.FamilyStatus)
	}
	if applier.seen.DeviceName != "apic-01" || len(applier.seen.ManagedFamilies) != 1 {
		t.Fatalf("applier intent=%#v", applier.seen)
	}
}
