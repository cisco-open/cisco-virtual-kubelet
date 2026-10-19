// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ValidateGroupCancellationFence proves a group journal no longer allows its
// Prepared-to-Submitting transition. It is not proof of member cleanup, absence
// of leases, or completed settlement. Partial/complete claims remain retained.
func ValidateGroupCancellationFence(nc *cvk.NetworkController, now time.Time) error {
	reject := errors.New("group cancellation fence is unavailable")
	if nc == nil || nc.UID == "" || nc.Status.Configuration == nil || now.IsZero() {
		return reject
	}
	op := nc.Status.Configuration.Operation
	if ValidateGroupJournalIdentity(op) != nil || op.Phase != "Cancelling" || op.CancellationRequestedAt == nil || op.CancellationRequestedAt.IsZero() || op.CancellationRequestedAt.After(now) || op.SubmittedAt != nil || op.TaskID != "" {
		return reject
	}
	return nil
}

// BeginGroupCancellation races submission using the SAME controller journal
// resourceVersion CAS. Only Prepared can enter Cancelling; Submitting/Unknown
// outcomes never become cleanup permission. Authorize must check installed group
// admission/recovery and actor identity. This staged primitive never releases
// leases, clears claims, settles members, or deletes the operation.
func BeginGroupCancellation(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, authorize func(context.Context, *cvk.NetworkController) error, now time.Time) error {
	reject := errors.New("group cannot be cancelled from this journal boundary")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || expected.Status.Configuration == nil || authorize == nil || now.IsZero() {
		return reject
	}
	original := expected.Status.Configuration.Operation
	if ValidateGroupJournalIdentity(original) != nil {
		return reject
	}
	read := func() (*cvk.NetworkController, error) {
		var nc cvk.NetworkController
		if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
			return nil, err
		}
		if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion || nc.Status.Configuration == nil || !equality.Semantic.DeepEqual(nc.Status.Configuration.Operation, original) {
			return nil, reject
		}
		return &nc, nil
	}
	nc, err := read()
	if err != nil {
		return err
	}
	if original.Phase != "Prepared" && original.Phase != "Cancelling" {
		return reject
	}
	if original.SubmittedAt != nil || original.TaskID != "" {
		return reject
	}
	if err := authorize(ctx, nc.DeepCopy()); err != nil {
		return err
	}
	nc, err = read()
	if err != nil {
		return err
	}
	if original.Phase == "Cancelling" {
		return ValidateGroupCancellationFence(nc, now)
	}
	if original.CancellationRequestedAt != nil {
		return reject
	}
	at := metav1.NewTime(now.UTC().Truncate(time.Second))
	nc.Status.Configuration.Operation.Phase = "Cancelling"
	nc.Status.Configuration.Operation.CancellationRequestedAt = &at
	desired := nc.Status.Configuration.Operation.DeepCopy()
	if err := writer.Status().Update(ctx, nc); err != nil {
		return err
	}
	var persisted cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &persisted); err != nil {
		return err
	}
	if persisted.UID != expected.UID || persisted.Status.Configuration == nil || !equality.Semantic.DeepEqual(persisted.Status.Configuration.Operation, desired) {
		return reject
	}
	return ValidateGroupCancellationFence(&persisted, now)
}
