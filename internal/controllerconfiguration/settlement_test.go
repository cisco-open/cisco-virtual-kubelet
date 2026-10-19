// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"testing"
	"time"
)

func settlementFixture(t *testing.T) (client.Client, *ops.ControllerConfigurationReservation, *cvk.NetworkController, time.Time) {
	t.Helper()
	_, r, op, now := claimFixture(t)
	r.Spec.Controller.Name = "cc"
	r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now.Add(-5 * time.Second))}
	op.Phase = "Verified"
	op.TaskID = "task"
	v := metav1.NewTime(now.Add(-4 * time.Second))
	op.SubmittedAt = &v
	nc := &cvk.NetworkController{ObjectMeta: metav1.ObjectMeta{Namespace: r.Namespace, Name: "cc", UID: types.UID(r.Spec.Controller.UID)}, Status: cvk.NetworkControllerStatus{Configuration: &cvk.NetworkControllerConfigurationStatus{ActionApplications: []cvk.NetworkControllerConfigurationActionApplication{{Section: op.Section, Resource: "stable-key", Operation: *op}}}}}
	scheme := runtime.NewScheme()
	_ = ops.AddToScheme(scheme)
	_ = cvk.AddToScheme(scheme)
	_ = coordination.AddToScheme(scheme)
	c := fake.NewClientBuilder().WithScheme(scheme).WithStatusSubresource(r, nc).WithObjects(r, nc).Build()
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	return c, r, nc, now
}
func TestSettlementLifecycle(t *testing.T) {
	ctx := context.Background()
	c, r, nc, now := settlementFixture(t)
	if MarkVerifiedReleased(ctx, c, c, r, now) == nil {
		t.Fatal("release before completion")
	}
	if err := PublishCompletion(ctx, c, c, r, now); err != nil {
		t.Fatal(err)
	}
	if MarkVerifiedReleased(ctx, c, c, r, now) == nil {
		t.Fatal("stale release overwrote evidence")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	if err := PublishCompletion(ctx, c, c, r, now.Add(time.Second)); err != nil {
		t.Fatal("completion retry must preserve original timestamp", err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	saved := nc.DeepCopy()
	nc.Status.Configuration.ActionApplications = nil
	if err := c.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	if MarkVerifiedReleased(ctx, c, c, r, now) == nil {
		t.Fatal("released without retained controller receipt")
	}
	nc.Status = saved.Status
	if err := c.Status().Update(ctx, nc); err != nil {
		t.Fatal(err)
	}
	holder := r.Status.Grant.Holder
	lease := &coordination.Lease{ObjectMeta: metav1.ObjectMeta{Namespace: r.Status.Grant.LeaseNamespace, Name: r.Status.Grant.Lease.Name, UID: types.UID(r.Status.Grant.Lease.UID)}, Spec: coordination.LeaseSpec{HolderIdentity: &holder}}
	if err := c.Create(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if MarkVerifiedReleased(ctx, c, c, r, now) == nil {
		t.Fatal("released while lease still held")
	}
	lease.Spec.HolderIdentity = nil
	if err := c.Update(ctx, lease); err != nil {
		t.Fatal(err)
	}
	if err := MarkVerifiedReleased(ctx, c, c, r, now); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	if r.Status.Grant.Phase != "Released" || r.Status.Completion == nil || r.Status.Claim == nil {
		t.Fatal("lost settlement evidence")
	}
}
func TestAmbiguousCompletionWriteRetainsEvidence(t *testing.T) {
	ctx := context.Background()
	c, r, _, now := settlementFixture(t)
	if PublishCompletion(ctx, c, ambiguousStatus{Client: c}, r, now) == nil {
		t.Fatal("ambiguous completion returned success")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	if r.Status.Completion == nil || r.Status.Grant.Phase != "Held" {
		t.Fatal("ambiguous completion released grant")
	}
	if err := PublishCompletion(ctx, c, c, r, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
}

func TestReleasedCompletionCanBeAuditedButNotInvented(t *testing.T) {
	ctx := context.Background()
	c, r, nc, now := settlementFixture(t)
	if err := c.Get(ctx, client.ObjectKeyFromObject(nc), nc); err != nil {
		t.Fatal(err)
	}
	completion, err := CompletionForReceipt(r, nc, now)
	if err != nil {
		t.Fatal(err)
	}
	r.Status.Grant.Phase = "Released"
	if err := c.Status().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := PublishCompletion(ctx, c, c, r, now); err == nil {
		t.Fatal("published new completion after release")
	}
	if err := c.Get(ctx, client.ObjectKeyFromObject(r), r); err != nil {
		t.Fatal(err)
	}
	if r.Status.Completion != nil {
		t.Fatal("rejected publication changed status")
	}
	r.Status.Completion = completion
	if err := c.Status().Update(ctx, r); err != nil {
		t.Fatal(err)
	}
	if err := ValidateCompletionReceipt(r, nc, now); err != nil {
		t.Fatal("released evidence cannot be audited", err)
	}
	if err := PublishCompletion(ctx, c, c, r, now); err != nil {
		t.Fatal("existing completion replay failed", err)
	}
}
