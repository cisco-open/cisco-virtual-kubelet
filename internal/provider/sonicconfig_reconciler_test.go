// Copyright (c) 2026 Cisco Systems Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.

package provider

import (
	"context"
	"testing"

	configv1alpha1 "github.com/cisco/virtual-kubelet-cisco/api/config/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/drivers/sonic"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

type fakeSONICConfigApplier struct {
	intent sonic.OpenConfigIntent
}

func (f *fakeSONICConfigApplier) Health(context.Context) error { return nil }

func (f *fakeSONICConfigApplier) Apply(_ context.Context, intent sonic.OpenConfigIntent) (sonic.ApplyResult, error) {
	f.intent = intent
	return sonic.ApplyResult{FamilyResults: []sonic.FamilyResult{{Name: intent.ManagedPaths[0], State: "InSync", Entries: 1, OpCount: int32(len(intent.Operations)), Message: "test applied"}}, Applied: len(intent.Operations) > 0}, nil
}

func (f *fakeSONICConfigApplier) Close() error { return nil }

func TestSONICConfigReconcilerAppliesOpenConfigIntent(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := configv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}
	cr := &configv1alpha1.SONICConfig{
		ObjectMeta: metav1.ObjectMeta{Name: "sonic-intent", Namespace: "default", Generation: 1},
		Spec: configv1alpha1.SONICConfigSpec{
			DeviceRef:    configv1alpha1.DeviceRef{Name: "sonic-8102-64h-01"},
			ManagedPaths: []string{"/interfaces"},
			ModelSource:  &configv1alpha1.NetAsCodeModelSource{Format: configv1alpha1.NetAsCodeModelFormatOpenConfig, Resolved: true},
			Source:       configv1alpha1.ConfigurationSource{Inline: &runtime.RawExtension{Raw: []byte(`{"openconfig":{"update":[{"path":"/interfaces/interface[name=Ethernet0]/config/description","value":"managed-by-cvk"}]}}`)}},
		},
	}
	client := fake.NewClientBuilder().WithScheme(scheme).WithObjects(cr).WithStatusSubresource(cr).Build()
	applier := &fakeSONICConfigApplier{}
	reconciler := &SONICConfigReconciler{Client: client, DeviceName: "sonic-8102-64h-01", Applier: applier}
	if err := reconciler.reconcileOne(context.Background(), cr); err != nil {
		t.Fatalf("reconcileOne: %v", err)
	}
	if len(applier.intent.Operations) != 1 || applier.intent.DriftPolicy != configv1alpha1.DriftPolicyRevert {
		t.Fatalf("unexpected intent: %#v", applier.intent)
	}
	var got configv1alpha1.SONICConfig
	if err := client.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: "sonic-intent"}, &got); err != nil {
		t.Fatalf("get updated SONICConfig: %v", err)
	}
	if got.Status.Phase != "InSync" || got.Status.LastAppliedHash == "" {
		t.Fatalf("unexpected status: %#v", got.Status)
	}
	if len(got.Status.FamilyStatus) != 1 || got.Status.FamilyStatus[0].Name != "/interfaces" || got.Status.FamilyStatus[0].OpCount != 1 {
		t.Fatalf("unexpected family status: %#v", got.Status.FamilyStatus)
	}
}

func TestValidateSONICModelSourceRejectsWrongFormat(t *testing.T) {
	cr := &configv1alpha1.SONICConfig{Spec: configv1alpha1.SONICConfigSpec{ModelSource: &configv1alpha1.NetAsCodeModelSource{Format: configv1alpha1.NetAsCodeModelFormatFMC}}}
	if err := validateSONICModelSource(cr); err == nil {
		t.Fatal("expected wrong modelSource format to fail")
	}
}
