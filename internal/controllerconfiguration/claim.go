// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ClaimOnce persists dispatch evidence with a resourceVersion CAS. There is no
// conflict retry: a competing revocation or claim requires reconciliation, and
// an existing claim never authorizes dispatch again. A write error is ambiguous
// and must not lead to remote submission, even if the claim actually persisted.
//
// This is a protocol primitive, not a complete authorizer. The caller must use
// an uncached reader and check actor admission and live source/device/worker
// bindings before calling; it must recheck live authority before remote dispatch.
func ClaimOnce(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *ops.ControllerConfigurationReservation, op *cvk.NetworkControllerConfigurationOperation, leaseNamespace string, now time.Time) error {
	if reader == nil || writer == nil || expected == nil || expected.ResourceVersion == "" {
		return errors.New("configuration claim requires versioned evidence")
	}
	var current ops.ControllerConfigurationReservation
	key := client.ObjectKeyFromObject(expected)
	if err := reader.Get(ctx, key, &current); err != nil {
		return err
	}
	if current.UID != expected.UID || current.ResourceVersion != expected.ResourceVersion || !reflect.DeepEqual(current.Spec, expected.Spec) || current.Status.Claim != nil {
		return errors.New("configuration reservation changed or is already claimed")
	}
	if err := ValidateOperationBinding(&current, op); err != nil {
		return err
	}
	if current.Status.Grant == nil {
		return errors.New("configuration reservation has no grant")
	}
	var lease coordination.Lease
	if err := reader.Get(ctx, client.ObjectKey{Namespace: leaseNamespace, Name: current.Status.Grant.Lease.Name}, &lease); err != nil {
		return err
	}
	if err := ValidateLiveGrant(&current, &lease, leaseNamespace, now); err != nil {
		return err
	}
	return persistConfigurationClaim(ctx, reader, writer, &current, op, now)
}

// Called only after protocol-specific live binding and grant validation. The
// observed object's resourceVersion is the CAS; ambiguous writes are not retried.
func persistConfigurationClaim(ctx context.Context, reader client.Reader, writer client.StatusClient, current *ops.ControllerConfigurationReservation, op *cvk.NetworkControllerConfigurationOperation, now time.Time) error {
	key := client.ObjectKeyFromObject(current)
	current.Status.Claim = &ops.ConfigurationReservationClaim{OperationID: op.ID, OperationDigest: current.Spec.OperationDigest, ClaimedAt: metav1.NewTime(now)}
	if err := writer.Status().Update(ctx, current); err != nil {
		return err
	}
	var persisted ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, key, &persisted); err != nil {
		return err
	}
	if persisted.UID != current.UID || persisted.DeletionTimestamp != nil || !reflect.DeepEqual(persisted.Spec, current.Spec) || !reflect.DeepEqual(persisted.Status, current.Status) {
		return errors.New("configuration claim persistence could not be verified")
	}
	return nil
}

// RevokeUnclaimed closes a grant with the same CAS used by ClaimOnce. A claimed
// reservation needs outcome recovery, never a preparation-only release. A
// revoked reservation cannot be regranted: a new operation needs a new object.
// The caller must be the admitted, bound device worker.
func RevokeUnclaimed(ctx context.Context, writer client.StatusClient, observed *ops.ControllerConfigurationReservation) error {
	if writer == nil || observed == nil || observed.UID == "" || observed.ResourceVersion == "" || observed.Spec.Protocol != Protocol || observed.Status.Claim != nil || observed.Status.Grant == nil || observed.Status.Grant.Phase != "Held" {
		return errors.New("configuration grant cannot be revoked as unclaimed")
	}
	updated := observed.DeepCopy()
	updated.Status.Grant.Phase = "Revoked"
	return writer.Status().Update(ctx, updated)
}
