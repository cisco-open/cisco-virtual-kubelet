// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package catalystcenter

import (
	"context"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Native evidence is synthetic: this isolates real schema, publication and CAS.
// The separate native test qualifies task correlation and job absence.
func TestEnvtestDiscoveryPruneReceiptPersistenceAndRelease(t *testing.T) {
	api := qualificationAPIServerClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const namespace = "discovery-prune"
	if err := api.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cc", Namespace: namespace}, Spec: cvk.NetworkControllerSpec{Type: TypeName, Endpoint: "https://cc.example.invalid", CredentialSecretRef: cvk.NetworkControllerSecretReference{Name: "credentials"}}}
	if err := api.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	at := metav1.NewTime(time.Now().UTC().Truncate(time.Second).Add(-time.Second))
	payload := map[string]any{"name": "fixture", "discoveryType": "SINGLE", "ipAddressList": "192.0.2.1", "protocolOrder": "SSH", "globalCredentialIdList": []string{"credential-id"}}
	creation := &cvk.NetworkControllerConfigurationOperation{ID: strings.Repeat("a", 32), ConfigName: "config", ConfigUID: "owner", ConfigGeneration: 1, ControllerGeneration: nc.Generation, Section: "inventory", Resource: ownedDiscoveryResource("fixture", payload), Action: "Create", Phase: "Verifying", TaskID: "create-task", SubmittedAt: &at}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: "inventory", ConfigName: creation.ConfigName, ConfigUID: creation.ConfigUID}}}
	if err := api.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	a := &adapter{statusReader: api, statusWriter: api, key: ctrlclient.ObjectKeyFromObject(nc), uid: nc.UID, generation: nc.Generation}
	publish := func(op *cvk.NetworkControllerConfigurationOperation) *cvk.NetworkController {
		t.Helper()
		stored, err := a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		stored.Status.Configuration.Operation = op.DeepCopy()
		stored.Status.Configuration.Operation.Phase = "Verifying"
		if err := api.Status().Update(ctx, stored); err != nil {
			t.Fatal(err)
		}
		stored, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		stored.Status.Configuration.Operation.Phase = "Verified"
		stamp := metav1.NewTime(time.Now().UTC().Truncate(time.Second))
		receipt := cvk.NetworkControllerConfigurationActionApplication{Section: "inventory", Resource: ownedDiscoveryReceiptKey("fixture", "42"), Operation: *stored.Status.Configuration.Operation.DeepCopy(), VerifiedAt: &stamp}
		stored.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{receipt}
		if err := a.persistConfigurationActionReceipt(ctx, stored); err != nil {
			t.Fatal("atomic publication", err)
		}
		stored, err = a.currentController(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if !ownedDiscoveryReceiptPresent(stored.Status.Configuration, stored.Status.Configuration.Operation) {
			t.Fatal("schema pruned discovery ownership")
		}
		return stored
	}
	stored := publish(creation)
	receipt := stored.Status.Configuration.ActionApplications[0]
	// The schema keys receipts by section/resource; a competing namespace can
	// therefore represent the same remote logical job twice. Release must fence it.
	duplicate := *receipt.DeepCopy()
	duplicate.Section, duplicate.Operation.Section = "templates", "templates"
	duplicate.Operation.Resource = "discoveries/" + duplicate.Operation.Resource
	duplicate.Operation.ID = strings.Repeat("d", 32)
	stored.Status.Configuration.ActionApplications = append(stored.Status.Configuration.ActionApplications, duplicate)
	if err := api.Status().Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, creation.ID); err == nil {
		t.Fatal("cross-namespace duplicate allowed release")
	}
	stored, _ = a.currentController(ctx)
	stored.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{receipt}
	if err := api.Status().Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	stored.Status.Configuration.ActionApplications = nil
	if err := api.Status().Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, creation.ID); err == nil {
		t.Fatal("pruned result allowed release")
	}
	stored, _ = a.currentController(ctx)
	stored.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{receipt}
	if err := a.persistConfigurationActionReceipt(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, creation.ID); err != nil {
		t.Fatal(err)
	}
	stored, _ = a.currentController(ctx)
	deletion := creation.DeepCopy()
	deletion.ID = strings.Repeat("b", 32)
	deletion.Action = "Delete"
	deletion.TargetID = "42"
	deletion.Resource += discoveryPruneSuffix
	deletion.ConfigGeneration = 2
	deletion.TaskID = "delete-task"
	if err := ownedDiscoveryCapacity(stored.Status.Configuration, deletion); err != nil {
		t.Fatal("delete lineage", err)
	}
	stored = publish(deletion)
	stale := stored.DeepCopy()
	stored.Status.Configuration.Operation.Phase = "Unknown"
	if err := api.Status().Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if err := a.persistConfigurationActionReceipt(ctx, stale); !apierrors.IsConflict(err) {
		t.Fatal("stale publication overwrote newer state", err)
	}
	stored, _ = a.currentController(ctx)
	stored.Status.Configuration.Operation = stored.Status.Configuration.ActionApplications[0].Operation.DeepCopy()
	if err := api.Status().Update(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if err := a.finishConfiguration(ctx, deletion.ID); err != nil {
		t.Fatal(err)
	}
	stored, _ = a.currentController(ctx)
	if stored.Status.Configuration.Operation != nil || len(stored.Status.Configuration.ActionApplications) != 1 || !strings.HasSuffix(stored.Status.Configuration.ActionApplications[0].Operation.Resource, discoveryPruneSuffix) || !validOwnedDiscoveryReceipt(stored.Status.Configuration.ActionApplications[0]) {
		t.Fatal("deletion tombstone lost")
	}
	recreate := creation.DeepCopy()
	recreate.ConfigGeneration = 2
	if err := ownedDiscoveryCapacity(stored.Status.Configuration, recreate); err == nil {
		t.Fatal("same-input recreation allowed")
	}
	recreate.ConfigGeneration = 3
	if err := ownedDiscoveryCapacity(stored.Status.Configuration, recreate); err != nil {
		t.Fatal("later explicit input refused", err)
	}
}
