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
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrlclient "sigs.k8s.io/controller-runtime/pkg/client"
)

// Native task evidence is synthetic here. The independent live test qualifies
// the CC APIs; this checks real schema/status/CAS/release semantics only.
func TestEnvtestIPAMOwnedResultPersistenceAndRelease(t *testing.T) {
	api := qualificationAPIServerClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	const namespace = "ipam-results"
	if err := api.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}); err != nil {
		t.Fatal(err)
	}
	for _, section := range []string{"network_settings", "fabric"} {
		t.Run(section, func(t *testing.T) {
			name := "cc-network"
			if section == "fabric" {
				name = "cc-fabric"
			}
			nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Spec: cvk.NetworkControllerSpec{Type: TypeName, Endpoint: "https://cc.example.invalid", CredentialSecretRef: cvk.NetworkControllerSecretReference{Name: "credentials"}}}
			if err := api.Create(ctx, nc); err != nil {
				t.Fatal(err)
			}
			receipt := ipamOwnershipReceiptFixture()
			// Match metav1.Time serialization before supplying synthetic native proof.
			receipt.Operation.SubmittedAt.Time = receipt.Operation.SubmittedAt.Time.UTC().Truncate(time.Second)
			receipt.VerifiedAt.Time = receipt.VerifiedAt.Time.UTC().Truncate(time.Second)
			receipt.Section = section
			receipt.Operation.Section = section
			receipt.Operation.ControllerGeneration = nc.Generation
			if section == "fabric" {
				receipt.Operation.Resource = "network_settings/" + receipt.Operation.Resource
			}
			receipt.Resource = ipamResultKey(receipt.Operation.Resource, ipamTestPoolID)
			op := receipt.Operation.DeepCopy()
			op.Phase = "Verifying"
			nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Owners: []cvk.NetworkControllerSectionOwner{{Section: section, ConfigName: op.ConfigName, ConfigUID: op.ConfigUID}}, Operation: op}
			if err := api.Status().Update(ctx, nc); err != nil {
				t.Fatal(err)
			}
			a := &adapter{statusReader: api, statusWriter: api, key: ctrlclient.ObjectKeyFromObject(nc), uid: nc.UID, generation: nc.Generation}
			current, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			current.Status.Configuration.Operation.Phase = "Verified"
			// Build proof from the same API-normalized operation the publisher verifies.
			receipt.Operation = *current.Status.Configuration.Operation.DeepCopy()
			current.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{receipt}
			if err := a.persistConfigurationActionReceipt(ctx, current); err != nil {
				t.Fatal("atomic result publication", err)
			}
			stored, err := a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !apiequality.Semantic.DeepEqual(stored.Status.Configuration.ActionApplications[0], receipt) || !ipamOwnedReceiptPresent(stored.Status.Configuration, stored.Status.Configuration.Operation) {
				t.Fatal("real API pruned or changed result binding")
			}
			stale := stored.DeepCopy()
			stored.Status.Configuration.Operation.Phase = "Unknown"
			if err := api.Status().Update(ctx, stored); err != nil {
				t.Fatal(err)
			}
			if err := a.persistConfigurationActionReceipt(ctx, stale); !apierrors.IsConflict(err) {
				t.Fatal("stale writer replaced newer status", err)
			}
			stored, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status.Configuration.Operation.Phase != "Unknown" || !apiequality.Semantic.DeepEqual(stored.Status.Configuration.ActionApplications[0], receipt) {
				t.Fatal("uncertain operation or result lost")
			}
			stored.Status.Configuration.Operation = receipt.Operation.DeepCopy()
			stored.Status.Configuration.ActionApplications = nil
			if err := api.Status().Update(ctx, stored); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, op.ID); err == nil {
				t.Fatal("missing atomic receipt allowed release")
			}
			stored, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			stored.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{receipt}
			if err := a.persistConfigurationActionReceipt(ctx, stored); err != nil {
				t.Fatal(err)
			}
			if err := a.finishConfiguration(ctx, op.ID); err != nil {
				t.Fatal("verified result did not release operation", err)
			}
			stored, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status.Configuration.Operation != nil || len(stored.Status.Configuration.ActionApplications) != 1 || !apiequality.Semantic.DeepEqual(stored.Status.Configuration.ActionApplications[0], receipt) {
				t.Fatal("result ownership lost on operation retirement")
			}

			// A deletion uses the same schema and atomic writer, retaining a tombstone.
			deletion := receipt.Operation.DeepCopy()
			deletion.ID = strings.Repeat("b", 32)
			deletion.Action = "Delete"
			deletion.TargetID = ipamTestPoolID
			deletion.ConfigGeneration = 2
			deletion.Phase = "Prepared"
			if err := ipamOwnedCapacity(stored.Status.Configuration, deletion); err != nil {
				t.Fatal("delete lineage", err)
			}
			deletion.Phase = "Verifying"
			deletion.TaskID = "delete-task"
			stored.Status.Configuration.Operation = deletion
			if err := api.Status().Update(ctx, stored); err != nil {
				t.Fatal(err)
			}
			stored, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			stored.Status.Configuration.Operation.Phase = "Verified"
			tombstone := receipt
			tombstone.Operation = *stored.Status.Configuration.Operation.DeepCopy()
			tombstone.VerifiedAt = &metav1.Time{Time: time.Now().UTC().Truncate(time.Second)}
			stored.Status.Configuration.ActionApplications = []cvk.NetworkControllerConfigurationActionApplication{tombstone}
			if err := a.persistConfigurationActionReceipt(ctx, stored); err != nil {
				t.Fatal("atomic deletion tombstone", err)
			}
			if err := a.finishConfiguration(ctx, deletion.ID); err != nil {
				t.Fatal("verified deletion retirement", err)
			}
			stored, err = a.currentController(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if stored.Status.Configuration.Operation != nil || !apiequality.Semantic.DeepEqual(stored.Status.Configuration.ActionApplications[0], tombstone) {
				t.Fatal("deletion tombstone pruned")
			}
			reintroduce := tombstone.Operation.DeepCopy()
			reintroduce.Action = "Create"
			reintroduce.TargetID = ""
			reintroduce.Phase = "Prepared"
			if err := ipamOwnedCapacity(stored.Status.Configuration, reintroduce); err == nil {
				t.Fatal("same-revision recreation allowed")
			}
			reintroduce.ConfigGeneration++
			if err := ipamOwnedCapacity(stored.Status.Configuration, reintroduce); err != nil {
				t.Fatal("explicit later source revision refused", err)
			}
		})
	}
}
