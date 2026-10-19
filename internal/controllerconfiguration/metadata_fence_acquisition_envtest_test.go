//go:build envtest

// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	admission "k8s.io/api/admissionregistration/v1"
	coordination "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type metadataAcquisitionAPIStore struct {
	client.Client
	mode             string
	creates, updates int
}

func (s *metadataAcquisitionAPIStore) Create(ctx context.Context, obj client.Object, opts ...client.CreateOption) error {
	if _, ok := obj.(*coordination.Lease); !ok {
		return errors.New("unexpected acquisition write")
	}
	s.creates++
	if err := s.Client.Create(ctx, obj, opts...); err != nil {
		return err
	}
	if s.mode == "create-lost-ack" {
		return errors.New("real Create response lost by client wrapper")
	}
	return nil
}
func (s *metadataAcquisitionAPIStore) Status() client.SubResourceWriter {
	return metadataAcquisitionAPIStatus{s.Client.Status(), s}
}

type metadataAcquisitionAPIStatus struct {
	client.SubResourceWriter
	store *metadataAcquisitionAPIStore
}

func (w metadataAcquisitionAPIStatus) Update(ctx context.Context, obj client.Object, opts ...client.SubResourceUpdateOption) error {
	w.store.updates++
	if err := w.SubResourceWriter.Update(ctx, obj, opts...); err != nil {
		return err
	}
	if w.store.mode == "status-lost-ack" {
		return errors.New("real status response lost by client wrapper")
	}
	return nil
}

// Real admission, server-assigned Lease UIDs and status CAS; actor readiness,
// compiler status and deployed settlement compatibility remain explicitly staged.
// No native API is called and no installed cluster or physical device is changed.
func qualifyMetadataAcquisitionOnAPI(t *testing.T, ctx context.Context, admin, manager client.Client, cfg *rest.Config, nc *cvk.NetworkController, metadataPolicyName string, serials []string) {
	t.Helper()
	_ = coordination.AddToScheme(admin.Scheme())
	policy, binding, _ := bootstrapProtectionFixture(t)
	for i := range policy.Spec.Variables {
		if policy.Spec.Variables[i].Name == "sharedNamespace" {
			policy.Spec.Variables[i].Expression = strings.ReplaceAll(policy.Spec.Variables[i].Expression, "bootstrap-leases", "metadata-leases")
		}
	}
	policy.UID = ""
	policy.ResourceVersion = ""
	policy.Generation = 0
	policy.Status = admission.ValidatingAdmissionPolicyStatus{}
	binding.UID = ""
	binding.ResourceVersion = ""
	binding.Generation = 0
	for _, obj := range []client.Object{policy, binding} {
		if err := admin.Create(ctx, obj); err != nil {
			t.Fatal(err)
		}
	}
	// An unbound admin has broad RBAC; only the actual admission policy rejects it.
	deadline := time.Now().Add(15 * time.Second)
	for {
		err := admin.Create(ctx, &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Name: "cvk-bootstrap-" + strings.Repeat("f", 32), Namespace: "metadata-leases"}}, client.DryRunAll)
		if (apierrors.IsForbidden(err) || apierrors.IsInvalid(err)) && strings.Contains(err.Error(), policy.Name) {
			break
		}
		if err != nil || time.Now().After(deadline) {
			t.Fatal("bootstrap acquisition admission not active", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(policy), policy); err != nil {
		t.Fatal(err)
	}
	policy.Status.ObservedGeneration = policy.Generation
	policy.Status.TypeChecking = &admission.TypeChecking{}
	if err := admin.Status().Update(ctx, policy); err != nil {
		t.Fatal(err)
	}
	installation := BootstrapFenceInstallation{PolicyName: policy.Name, ManagerUsername: bootstrapTestManager, LeaseNamespace: "metadata-leases"}
	compatible := func(context.Context, *cvk.NetworkController, MetadataScopeWorkerObservation, AdmissionProtection) error {
		return nil
	}
	store := &metadataAcquisitionAPIStore{Client: manager, mode: "create-lost-ack"}
	if _, err := AcquireMetadataFenceCohort(ctx, admin, store, nc, installation, metadataPolicyName, serials, compatible, time.Now()); err == nil || store.creates != 1 || store.updates != 0 {
		t.Fatal("real create lost-response boundary", err, store.creates, store.updates)
	}
	first, _ := BootstrapFenceName(serials[0])
	var original coordination.Lease
	// The acquisition order is canonical even if the supplied cohort is reversed.
	if len(serials) > 1 && serials[0] > serials[1] {
		first, _ = BootstrapFenceName(serials[1])
	}
	if err := admin.Get(ctx, client.ObjectKey{Namespace: "metadata-leases", Name: first}, &original); err != nil || original.UID == "" {
		t.Fatal("original server UID missing", err)
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	store.mode = "status-lost-ack"
	if _, err := AcquireMetadataFenceCohort(ctx, admin, store, nc, installation, metadataPolicyName, serials, compatible, time.Now()); err == nil || store.creates != 1 || store.updates != 1 {
		t.Fatal("real status lost-response boundary", err, store.creates, store.updates)
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	refs := nc.Status.Configuration.MetadataHandoff.Fences
	if len(refs) != 1 || refs[0].UID != string(original.UID) {
		t.Fatal("original UID not retained after lost status response")
	}
	store.mode = "valid"
	current, err := AcquireMetadataFenceCohort(ctx, admin, store, nc, installation, metadataPolicyName, serials, compatible, time.Now())
	if err != nil || current == nil || store.creates != len(serials) || store.updates != len(serials)+1 {
		t.Fatal("real partial acquisition recovery", err, store.creates, store.updates)
	}
	if _, err := ReadMetadataFenceCohort(ctx, admin, current); err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireMetadataFenceCohort(ctx, admin, store, current, installation, metadataPolicyName, serials, compatible, time.Now()); err != nil || store.creates != len(serials) || store.updates != len(serials)+1 {
		t.Fatal("real completed acquisition wrote again", err)
	}
	// Presence remains an enrollment hold and cannot be adopted by the public
	// absence-only observer, even though the private acquisition recognizes it.
	if err := CheckBootstrapEnrollment(ctx, admin, "metadata-leases", serials[0]); err == nil {
		t.Fatal("retained metadata custody allowed enrollment")
	}
	if err := CheckUnmanagedMetadataScope(ctx, admin, current, "metadata-leases", serials); err == nil {
		t.Fatal("public observer bypassed retained custody")
	}
	if current.Status.Configuration.Operation.Phase != "Prepared" || current.Status.Configuration.Operation.SubmittedAt != nil || current.Status.Configuration.MetadataHandoff.ReleasedAt != nil {
		t.Fatal("acquisition crossed submission or release boundary")
	}
	qualifyMetadataClaimOnAPI(t, ctx, cfg, admin, current, installation, metadataPolicyName)
	t.Log("real manager admission acquired original server Lease UIDs and status references, recovered lost Create/status responses without duplicate creation, and observed completion idempotently; no native calls; worker readiness/compiler status/settlement compatibility staged")
}
