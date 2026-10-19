// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build envtest

package controllerconfiguration

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	core "k8s.io/api/core/v1"
	rbac "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
)

func TestReservationAdmission(t *testing.T) {
	env := &envtest.Environment{CRDDirectoryPaths: []string{"../../config/crd/ops.cisco.vk_controllerconfigurationreservations.yaml", "../../config/crd/cisco.vk_networkcontrollers.yaml"}, ErrorIfCRDPathMissing: true}
	cfg, err := env.Start()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := env.Stop(); err != nil {
			t.Error(err)
		}
	}()
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = ops.AddToScheme(scheme)
	_ = cvk.AddToScheme(scheme)
	admin, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	const ns = "reservation-test"
	if err = admin.Create(ctx, &core.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}}); err != nil {
		t.Fatal(err)
	}
	testGroupJournalAPIServer(t, ctx, admin, ns)
	raw, err := exec.CommandContext(ctx, "helm", "template", "cvk", "../../charts/cisco-virtual-kubelet", "--namespace", ns, "--kube-version", "1.35.0", "--set", "topology.enabled=true", "--set", "controller.leaderElect=true", "--set", "rbac.profile=strict", "--show-only", "templates/configuration-reservation-admission.yaml").CombinedOutput()
	if err != nil {
		t.Fatalf("helm %v %s", err, raw)
	}
	decoder := yamlutil.NewYAMLOrJSONDecoder(bytes.NewReader(raw), 4096)
	for {
		var obj unstructured.Unstructured
		err = decoder.Decode(&obj.Object)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = admin.Create(ctx, &obj); err != nil {
			t.Fatal(err)
		}
	}
	callers := map[string]client.Client{}
	for _, name := range []string{"cisco-virtual-kubelet-controller", "xe", "cc", "other"} {
		username := "system:serviceaccount:" + ns + ":" + name
		role := &rbac.Role{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, Rules: []rbac.PolicyRule{{APIGroups: []string{"ops.cisco.vk"}, Resources: []string{"controllerconfigurationreservations", "controllerconfigurationreservations/status"}, Verbs: []string{"get", "create", "update", "delete"}}}}
		if err = admin.Create(ctx, role); err != nil {
			t.Fatal(err)
		}
		if err = admin.Create(ctx, &rbac.RoleBinding{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns}, RoleRef: rbac.RoleRef{APIGroup: rbac.GroupName, Kind: "Role", Name: name}, Subjects: []rbac.Subject{{Kind: "User", APIGroup: rbac.GroupName, Name: username}}}); err != nil {
			t.Fatal(err)
		}
		cc := rest.CopyConfig(cfg)
		cc.Impersonate = rest.ImpersonationConfig{UserName: username, Extra: map[string][]string{"authentication.kubernetes.io/pod-uid": {name + "-pod"}}}
		callers[name], err = client.New(cc, client.Options{Scheme: scheme})
		if err != nil {
			t.Fatal(err)
		}
	}
	_, r, _, _ := claimFixture(t)
	r.Namespace = ns
	r.UID = ""
	r.ResourceVersion = ""
	r.Status = ops.ControllerConfigurationReservationStatus{}
	r.Finalizers = []string{Finalizer}
	s := &r.Spec
	s.Controller.Name = "cc"
	s.Node = ops.ConfigurationReservationReference{Name: "node", UID: "node-uid"}
	s.ControllerWorkerPod = ops.ConfigurationReservationReference{Name: "cc", UID: "cc-pod"}
	s.DeviceWorkerPod = ops.ConfigurationReservationReference{Name: "xe", UID: "xe-pod"}
	s.ControllerUsername = "system:serviceaccount:" + ns + ":cc"
	s.DeviceWorkerUsername = "system:serviceaccount:" + ns + ":xe"
	s.DeviceGeneration = 1
	s.PhysicalIdentity = "serial"
	s.ManagementAddress = "192.0.2.1"
	s.DeviceWorkerRevision = "revision"
	denied := func(err error) {
		t.Helper()
		if !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
			t.Fatalf("expected admission denial, got %v", err)
		}
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		err = callers["other"].Create(ctx, r.DeepCopy(), client.DryRunAll)
		if apierrors.IsForbidden(err) || apierrors.IsInvalid(err) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("policy not active: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	manager := callers["cisco-virtual-kubelet-controller"]
	if err = manager.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	get := func() *ops.ControllerConfigurationReservation {
		t.Helper()
		v := &ops.ControllerConfigurationReservation{}
		if e := admin.Get(ctx, client.ObjectKeyFromObject(r), v); e != nil {
			t.Fatal(e)
		}
		return v
	}
	v := get()
	now := metav1.Now()
	invalidGroupMarker := v.DeepCopy()
	invalidGroupMarker.Status.GroupCancellation = &ops.ConfigurationReservationGroupCancellation{OperationID: v.Spec.OperationID, OperationDigest: v.Spec.OperationDigest, RequestedAt: now, AcknowledgedAt: now}
	if err := admin.Status().Update(ctx, invalidGroupMarker, client.DryRunAll); !apierrors.IsInvalid(err) {
		t.Fatalf("v1 reservation accepted group-only cancellation marker: %v", err)
	}
	v.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: "lease", UID: "lease-uid"}, Holder: "holder", LeaseTransitions: 1, GrantedAt: now}
	denied(callers["cc"].Status().Update(ctx, v.DeepCopy()))
	wrongPodConfig := rest.CopyConfig(cfg)
	wrongPodConfig.Impersonate = rest.ImpersonationConfig{UserName: s.DeviceWorkerUsername, Extra: map[string][]string{"authentication.kubernetes.io/pod-uid": {"replacement-pod"}}}
	wrongPod, err := client.New(wrongPodConfig, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatal(err)
	}
	denied(wrongPod.Status().Update(ctx, v.DeepCopy()))
	testGroupReservationAdmission(t, ctx, admin, manager, callers["xe"], callers["cc"], wrongPod, r)
	if err = callers["xe"].Status().Update(ctx, v); err != nil {
		t.Fatal(err)
	}
	v = get()
	v.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: v.Spec.OperationID, OperationDigest: v.Spec.OperationDigest, ClaimedAt: now}
	denied(callers["xe"].Status().Update(ctx, v.DeepCopy()))
	if err = callers["cc"].Status().Update(ctx, v); err != nil {
		t.Fatal(err)
	}
	v = get()
	v.Status.Claim = nil
	denied(callers["cc"].Status().Update(ctx, v))
	v = get()
	v.Status.Grant.Phase = "Revoked"
	denied(callers["xe"].Status().Update(ctx, v))
	v = get()
	denied(manager.Delete(ctx, v))
	v.Finalizers = nil
	denied(manager.Update(ctx, v))
	v = get()
	v.Status.Grant.Phase = "Released"
	denied(callers["xe"].Status().Update(ctx, v))
	v = get()
	v.Status.Completion = &ops.ConfigurationReservationCompletion{OperationID: v.Spec.OperationID, OperationDigest: v.Spec.OperationDigest, TaskID: "task", SubmittedAt: now, VerifiedAt: now}
	denied(callers["xe"].Status().Update(ctx, v.DeepCopy()))
	badCompletion := v.DeepCopy()
	badCompletion.Status.Completion.OperationID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	denied(callers["cc"].Status().Update(ctx, badCompletion))
	if err = callers["cc"].Status().Update(ctx, v); err != nil {
		t.Fatal(err)
	}
	v = get()
	v.Status.Completion = nil
	denied(callers["cc"].Status().Update(ctx, v))
	v = get()
	denied(manager.Delete(ctx, v))
	v.Status.Grant.Phase = "Released"
	denied(callers["cc"].Status().Update(ctx, v.DeepCopy()))
	if err = callers["xe"].Status().Update(ctx, v); err != nil {
		t.Fatal(err)
	}
	v = get()
	denied(manager.Delete(ctx, v.DeepCopy()))
	premature := v.DeepCopy()
	premature.Finalizers = nil
	denied(manager.Update(ctx, premature))
	v.Status.ManagerSettledAt = now.DeepCopy()
	denied(callers["xe"].Status().Update(ctx, v.DeepCopy()))
	denied(callers["cc"].Status().Update(ctx, v.DeepCopy()))
	if err = manager.Status().Update(ctx, v); err != nil {
		t.Fatal(err)
	}
	v = get()
	erased := v.DeepCopy()
	erased.Status.ManagerSettledAt = nil
	denied(manager.Status().Update(ctx, erased))
	changed := v.DeepCopy()
	changed.Status.ManagerSettledAt = &metav1.Time{Time: now.Add(time.Second)}
	denied(manager.Status().Update(ctx, changed))
	v.Finalizers = nil
	if err = manager.Update(ctx, v); err != nil {
		t.Fatal(err)
	}
	if err = manager.Delete(ctx, v); err != nil {
		t.Fatal(err)
	}
	// A separate, never-claimed reservation can be revoked and released.
	prep := r.DeepCopy()
	prep.Name = "preparation-only"
	prep.UID = ""
	prep.ResourceVersion = ""
	prep.Status = ops.ControllerConfigurationReservationStatus{}
	if err = manager.Create(ctx, prep); err != nil {
		t.Fatal(err)
	}
	refreshPrep := func() {
		t.Helper()
		if err := admin.Get(ctx, client.ObjectKeyFromObject(prep), prep); err != nil {
			t.Fatal(err)
		}
	}
	refreshPrep()
	prep.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: "lease", UID: "lease-uid"}, Holder: "holder", LeaseTransitions: 1, GrantedAt: now}
	if err = callers["xe"].Status().Update(ctx, prep); err != nil {
		t.Fatal(err)
	}
	refreshPrep()
	prep.Status.Grant.Phase = "Released"
	denied(callers["xe"].Status().Update(ctx, prep.DeepCopy()))
	prep.Status.Grant.Phase = "Revoked"
	if err = callers["xe"].Status().Update(ctx, prep); err != nil {
		t.Fatal(err)
	}
	refreshPrep()
	prep.Status.Grant.Phase = "Held"
	denied(callers["xe"].Status().Update(ctx, prep.DeepCopy()))
	prep.Status.Grant.Phase = "Released"
	if err = callers["xe"].Status().Update(ctx, prep); err != nil {
		t.Fatal(err)
	}
	refreshPrep()
	denied(manager.Delete(ctx, prep.DeepCopy()))
	prep.Status.ManagerSettledAt = now.DeepCopy()
	if err = manager.Status().Update(ctx, prep); err != nil {
		t.Fatal(err)
	}
	refreshPrep()
	prep.Finalizers = nil
	if err = manager.Update(ctx, prep); err != nil {
		t.Fatal(err)
	}
	if err = manager.Delete(ctx, prep); err != nil {
		t.Fatal(err)
	}

	// Cancelling before any grant is a distinct, one-way device-worker action.
	cancelled := r.DeepCopy()
	cancelled.Name = "cancelled-preparation"
	cancelled.UID = ""
	cancelled.ResourceVersion = ""
	cancelled.Generation = 0
	cancelled.DeletionTimestamp = nil
	cancelled.Finalizers = []string{Finalizer}
	cancelled.Status = ops.ControllerConfigurationReservationStatus{}
	if err = manager.Create(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	refreshCancelled := func() {
		t.Helper()
		if err := admin.Get(ctx, client.ObjectKeyFromObject(cancelled), cancelled); err != nil {
			t.Fatal(err)
		}
	}
	refreshCancelled()
	cancelled.Status.PreparationCancelledAt = now.DeepCopy()
	denied(callers["cc"].Status().Update(ctx, cancelled.DeepCopy()))
	denied(wrongPod.Status().Update(ctx, cancelled.DeepCopy()))
	if err = callers["xe"].Status().Update(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	refreshCancelled()
	denied(manager.Delete(ctx, cancelled.DeepCopy()))
	grantAttempt := cancelled.DeepCopy()
	grantAttempt.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: "lease", UID: "lease-uid"}, Holder: "holder", LeaseTransitions: 1, GrantedAt: now}
	denied(callers["xe"].Status().Update(ctx, grantAttempt))
	clearAttempt := cancelled.DeepCopy()
	clearAttempt.Status.PreparationCancelledAt = nil
	denied(callers["xe"].Status().Update(ctx, clearAttempt))
	cancelled.Status.ManagerSettledAt = now.DeepCopy()
	denied(callers["xe"].Status().Update(ctx, cancelled.DeepCopy()))
	if err = manager.Status().Update(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	refreshCancelled()
	cancelled.Finalizers = nil
	if err = manager.Update(ctx, cancelled); err != nil {
		t.Fatal(err)
	}
	if err = manager.Delete(ctx, cancelled); err != nil {
		t.Fatal(err)
	}

}
