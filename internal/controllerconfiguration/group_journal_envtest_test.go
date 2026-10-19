// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
//go:build envtest

package controllerconfiguration

import (
	"context"
	"reflect"
	"testing"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func testGroupJournalAPIServer(t *testing.T, ctx context.Context, admin client.Client, ns string) {
	t.Helper()
	testGroupCancellationAPIServer(t, ctx, admin, ns)
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "group-journal", Namespace: ns}, Spec: cvk.NetworkControllerSpec{Type: "catalyst-center", Endpoint: "https://catc.example", CredentialSecretRef: cvk.NetworkControllerSecretReference{Name: "credentials"}}}
	if err := admin.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	op, targets := groupIdentityFixture()
	prepared, err := WithGroupIdentity(op, targets)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: prepared}
	if err := admin.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	reload := func() *cvk.NetworkController {
		t.Helper()
		var current cvk.NetworkController
		if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), &current); err != nil {
			t.Fatal(err)
		}
		return &current
	}
	current := reload()
	if !reflect.DeepEqual(prepared, current.Status.Configuration.Operation) || ValidateGroupJournalIdentity(current.Status.Configuration.Operation) != nil {
		t.Fatal("API server pruned group identity")
	}
	for _, scenario := range []string{"mixed", "empty", "duplicate", "version", "digest", "missing-uid"} {
		changed := reload()
		pending := changed.Status.Configuration.Operation
		switch scenario {
		case "mixed":
			pending.TargetSerial = "serial"
		case "empty":
			pending.PhysicalGroup.Targets = nil
		case "duplicate":
			pending.PhysicalGroup.Targets = append(pending.PhysicalGroup.Targets, pending.PhysicalGroup.Targets[0])
		case "version":
			pending.PhysicalGroup.Version = "future"
		case "digest":
			pending.PhysicalGroup.Digest = "invalid"
		case "missing-uid":
			pending.PhysicalGroup.Targets[0].Node.UID = ""
		}
		if err := admin.Status().Update(ctx, changed); !apierrors.IsInvalid(err) {
			t.Fatalf("%s invalid group accepted: %v", scenario, err)
		}
	}
	// Group candidates must survive the real schema. Actor permissions are
	// tested separately after installing the rendered admission policy.
	candidateController, members, connector, now := groupCandidateFixture(t)
	candidates, err := GroupReservationCandidates(candidateController, members, connector, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range candidates {
		candidate.Namespace = ns
		if err := admin.Create(ctx, candidate, client.DryRunAll); err != nil {
			t.Fatalf("group reservation schema rejected candidate: %v", err)
		}
	}
	current = reload()
	current.Status.Configuration.Operation.Phase = "Accepted"
	current.Status.Configuration.Operation.TaskID = "task"
	if err := admin.Status().Update(ctx, current); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGroupJournalIdentity(reload().Status.Configuration.Operation); err != nil {
		t.Fatal(err)
	}
}

func testGroupCancellationAPIServer(t *testing.T, ctx context.Context, admin client.Client, ns string) {
	t.Helper()
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Name: "cancel-journal", Namespace: ns}, Spec: cvk.NetworkControllerSpec{Type: "catalyst-center", Endpoint: "https://catc.example", CredentialSecretRef: cvk.NetworkControllerSecretReference{Name: "credentials"}}}
	if err := admin.Create(ctx, nc); err != nil {
		t.Fatal(err)
	}
	op, targets := groupIdentityFixture()
	bound, err := WithGroupIdentity(op, targets)
	if err != nil {
		t.Fatal(err)
	}
	nc.Status.Configuration = &cvk.NetworkControllerConfigurationStatus{Operation: bound}
	if err := admin.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := BeginGroupCancellation(ctx, admin, admin, nc, func(context.Context, *cvk.NetworkController) error { return nil }, now); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	if err := ValidateGroupCancellationFence(nc, now); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"resume", "submit", "change-time", "drop-group"} {
		changed := nc.DeepCopy()
		operation := changed.Status.Configuration.Operation
		switch mode {
		case "resume":
			operation.Phase = "Prepared"
			operation.CancellationRequestedAt = nil
		case "submit":
			operation.Phase = "Submitting"
			operation.CancellationRequestedAt = nil
			at := metav1.NewTime(now)
			operation.SubmittedAt = &at
		case "change-time":
			at := metav1.NewTime(now.Add(time.Hour))
			operation.CancellationRequestedAt = &at
		case "drop-group":
			operation.PhysicalGroup = nil
		}
		if err := admin.Status().Update(ctx, changed); !apierrors.IsInvalid(err) {
			t.Fatalf("%s cancellation reversal allowed: %v", mode, err)
		}
	}
	// Schema persistence only: group reservations remain disabled, so actual
	// member cleanup/closure is exercised separately by the lifecycle fixtures.
	receipt := cvk.NetworkControllerConfigurationGroupCancellation{
		OperationID: nc.Status.Configuration.Operation.ID,
		Operation:   *nc.Status.Configuration.Operation.DeepCopy(),
		Members:     []ops.ConfigurationReservationReference{{Name: "member", UID: "member-uid"}},
		SettledAt:   metav1.NewTime(now.UTC().Truncate(time.Second)),
	}
	nc.Status.Configuration.GroupCancellations = []cvk.NetworkControllerConfigurationGroupCancellation{receipt}
	nc.Status.Configuration.Operation = nil
	if err := admin.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if err := admin.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	if len(nc.Status.Configuration.GroupCancellations) != 1 || !equality.Semantic.DeepEqual(nc.Status.Configuration.GroupCancellations[0], receipt) {
		t.Fatal("cancellation receipt was pruned or changed")
	}
	for _, mode := range []string{"member", "time", "operation"} {
		changed := nc.DeepCopy()
		retained := &changed.Status.Configuration.GroupCancellations[0]
		switch mode {
		case "member":
			retained.Members[0].UID = "replacement"
		case "time":
			retained.SettledAt = metav1.NewTime(now.Add(time.Hour))
		case "operation":
			retained.Operation.Resource = "other"
		}
		if err := admin.Status().Update(ctx, changed); !apierrors.IsInvalid(err) {
			t.Fatalf("%s changed retained cancellation: %v", mode, err)
		}
	}
}
