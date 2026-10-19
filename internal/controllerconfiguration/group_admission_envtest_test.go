// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
//go:build envtest

package controllerconfiguration

import (
	"context"
	"testing"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// Exercise the rendered policy and CRD together, with real impersonated actors.
// Runtime authorization still independently corroborates the journal and leases.
func testGroupReservationAdmission(t *testing.T, ctx context.Context, admin, manager, worker, connector, wrongPod client.Client, template *ops.ControllerConfigurationReservation) {
	for _, mode := range []string{"ungranted", "held", "revoked", "claimed", "verified"} {
		t.Run("group-"+mode, func(t *testing.T) {
			row := template.DeepCopy()
			row.Name = "group-admission-" + mode
			row.ResourceVersion = ""
			row.UID = ""
			row.Spec.Protocol = GroupReservationProtocol
			row.Status = ops.ControllerConfigurationReservationStatus{}
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			deny := func(err error) {
				t.Helper()
				if !apierrors.IsForbidden(err) && !apierrors.IsInvalid(err) {
					t.Fatalf("expected admission denial: %v", err)
				}
			}
			refresh := func() { t.Helper(); must(admin.Get(ctx, client.ObjectKeyFromObject(row), row)) }
			mutate := func(actor client.Client, change func(*ops.ControllerConfigurationReservation), allowed bool) {
				t.Helper()
				refresh()
				copy := row.DeepCopy()
				change(copy)
				err := actor.Status().Update(ctx, copy)
				if allowed {
					must(err)
				} else {
					deny(err)
				}
			}
			deny(worker.Create(ctx, row.DeepCopy()))
			must(manager.Create(ctx, row))
			refresh()
			deny(manager.Delete(ctx, row.DeepCopy()))
			stripped := row.DeepCopy()
			stripped.Finalizers = nil
			deny(manager.Update(ctx, stripped))
			now := metav1.Now()
			grant := func(r *ops.ControllerConfigurationReservation) {
				r.Status.Grant = &ops.ConfigurationReservationGrant{Phase: "Held", LeaseNamespace: "leases", Lease: ops.ConfigurationReservationReference{Name: "lease", UID: "lease-uid"}, Holder: "holder", LeaseTransitions: 1, GrantedAt: now}
			}
			claim := func(r *ops.ControllerConfigurationReservation) {
				r.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, ClaimedAt: now}
			}
			acknowledge := func(r *ops.ControllerConfigurationReservation) {
				r.Status.GroupCancellation = &ops.ConfigurationReservationGroupCancellation{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, RequestedAt: now, AcknowledgedAt: now}
			}
			settle := func(r *ops.ControllerConfigurationReservation) { r.Status.ManagerSettledAt = now.DeepCopy() }
			complete := func(r *ops.ControllerConfigurationReservation) {
				r.Status.Completion = &ops.ConfigurationReservationCompletion{OperationID: r.Spec.OperationID, OperationDigest: r.Spec.OperationDigest, TaskID: "task", SubmittedAt: now, VerifiedAt: now}
			}
			release := func(r *ops.ControllerConfigurationReservation) { r.Status.Grant.Phase = "Released" }
			mutate(worker, func(r *ops.ControllerConfigurationReservation) { r.Status.PreparationCancelledAt = now.DeepCopy() }, false)
			mutate(manager, settle, false)
			if mode != "ungranted" {
				mutate(connector, grant, false)
				mutate(worker, grant, true)
			}
			if mode == "revoked" {
				mutate(worker, func(r *ops.ControllerConfigurationReservation) { r.Status.Grant.Phase = "Revoked" }, true)
			}
			if mode == "claimed" || mode == "verified" {
				mutate(worker, claim, false)
				mutate(connector, claim, true)
			}
			if mode != "ungranted" {
				mutate(worker, release, false)
			}
			if mode == "verified" {
				mutate(worker, release, false)
				mutate(worker, complete, false)
				mutate(connector, complete, true)
				mutate(worker, acknowledge, false)
				mutate(worker, release, true)
			} else {
				mutate(manager, acknowledge, false)
				mutate(connector, acknowledge, false)
				mutate(wrongPod, acknowledge, false)
				mutate(worker, func(r *ops.ControllerConfigurationReservation) {
					acknowledge(r)
					r.Status.GroupCancellation.OperationID = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
				}, false)
				mutate(worker, func(r *ops.ControllerConfigurationReservation) { acknowledge(r); settle(r) }, false)
				if mode == "ungranted" {
					mutate(worker, func(r *ops.ControllerConfigurationReservation) { acknowledge(r); grant(r) }, false)
				}
				mutate(worker, acknowledge, true)
				mutate(worker, func(r *ops.ControllerConfigurationReservation) { r.Status.GroupCancellation = nil }, false)
				mutate(connector, complete, false)
				if mode != "claimed" {
					mutate(connector, claim, false)
				}
				if mode == "ungranted" {
					mutate(worker, grant, false)
				} else {
					mutate(manager, settle, false)
					mutate(connector, release, false)
					mutate(worker, func(r *ops.ControllerConfigurationReservation) { release(r); r.Status.Grant.Holder = "other" }, false)
					mutate(worker, release, true)
					mutate(worker, func(r *ops.ControllerConfigurationReservation) { r.Status.Grant.Phase = "Held" }, false)
				}
			}
			refresh()
			deny(manager.Delete(ctx, row.DeepCopy()))
			mutate(worker, settle, false)
			mutate(connector, settle, false)
			mutate(manager, settle, true)
			mutate(manager, func(r *ops.ControllerConfigurationReservation) { r.Status.ManagerSettledAt = nil }, false)
			refresh()
			row.Finalizers = nil
			must(manager.Update(ctx, row))
			must(manager.Delete(ctx, row))
		})
	}
}
