// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build envtest

package catalystcenter

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Real isolated API-server persistence/admission only. No controller or device
// runs here; the completion evidence is synthetic, unlike the separate CC test.
func TestEnvtestSSIDCredentialHistoryPersistence(t *testing.T) {
	api := qualificationAPIServerClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const namespace = "ssid-history"
	if err := api.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: namespace}, Spec: cvk.NetworkControllerSpec{Type: "catalyst-center", Endpoint: "https://cc.example.invalid", CredentialSecretRef: cvk.NetworkControllerSecretReference{Name: "credentials"}}}
	if err := api.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	history := []cvk.NetworkControllerConfigurationSSIDCredentialHistory{{Priority: 0, Version: 1}, {Priority: 4, Version: 3}}
	versions := []cvk.NetworkControllerConfigurationCredentialVersion{{Key: "passphrase", Path: "/ssids/0/passphrase", Version: 1, SourceAlias: "main", SecretName: "keys", SecretKey: "main"}}
	resource := ssidHistoryPrefix(history) + ":global:CVK"
	operation := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "ssid", ConfigUID: "config-uid", ConfigGeneration: 1, ControllerGeneration: nc.Generation, Section: "wireless", Resource: resource, Action: "Update", Phase: "Prepared", TargetID: "ssid-id", ParentID: "global", CredentialVersions: versions, SSIDCredentialHistory: history}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: operation, CredentialApplications: []cvk.NetworkControllerConfigurationCredentialApplication{{Section: "wireless", Resource: resource, ConfigUID: "config-uid", TargetID: "ssid-id", OperationID: operation.ID, Versions: versions, SSIDCredentialHistory: history}}}
	if err := api.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	var stored cvk.NetworkController
	if err := api.Get(ctx, ctrlclient.ObjectKeyFromObject(nc), &stored); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored.Status.Configuration.Operation.SSIDCredentialHistory, history) || !slices.Equal(stored.Status.Configuration.CredentialApplications[0].SSIDCredentialHistory, history) {
		t.Fatal("real API pruned history")
	}
	if err := validateSSIDCredentialHistory("wireless", resource, stored.Status.Configuration.Operation.CredentialVersions, stored.Status.Configuration.Operation.SSIDCredentialHistory); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]cvk.NetworkControllerConfigurationSSIDCredentialHistory{{{Priority: 5, Version: 1}}, {{Priority: 4, Version: 0}}, {{Priority: 0, Version: 1}, {Priority: 0, Version: 2}}} {
		modified := stored.DeepCopy()
		modified.Status.Configuration.Operation.SSIDCredentialHistory = bad
		if err := api.Status().Update(ctx, modified); !apierrors.IsInvalid(err) {
			t.Fatal("invalid history admitted", err)
		}
	}
	t.Log("real API preserves operation/receipt history and rejects invalid priorities, versions and duplicate map keys; no device API used")
}
