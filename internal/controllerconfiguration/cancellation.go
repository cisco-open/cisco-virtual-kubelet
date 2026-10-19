// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// CancelPreparation atomically closes new grant publication before Lease and
// maintenance cleanup. It never releases a Lease, fabricates a grant, clears a
// controller journal or settles manager evidence. A competing grant/status CAS
// wins instead of being overwritten. Call only as the bound device Pod.
func CancelPreparation(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *ops.ControllerConfigurationReservation, podUID string, now time.Time) error {
	reject := errors.New("configuration preparation cannot be cancelled from this evidence")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || podUID == "" || now.IsZero() {
		return reject
	}
	var r ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &r); err != nil {
		return err
	}
	if r.UID != expected.UID || r.ResourceVersion != expected.ResourceVersion || !reflect.DeepEqual(r.Spec, expected.Spec) || r.Spec.Protocol != Protocol || r.Spec.DeviceWorkerPod.UID != podUID || r.Status.Grant != nil || r.Status.Claim != nil || r.Status.Completion != nil {
		return reject
	}
	if r.Status.PreparationCancelledAt != nil {
		if r.Status.PreparationCancelledAt.IsZero() || r.Status.PreparationCancelledAt.After(now) {
			return reject
		}
		return nil
	}
	if r.Status.ManagerSettledAt != nil {
		return reject
	}
	r.Status.PreparationCancelledAt = &metav1.Time{Time: now}
	if err := writer.Status().Update(ctx, &r); err != nil {
		return err
	}
	return verifySettlementPersistence(ctx, reader, &r)
}

// ValidatePreparationCancellation checks the terminal pre-grant marker. This
// proves that no dispatch is possible, not that maintenance has been settled.
func ValidatePreparationCancellation(r *ops.ControllerConfigurationReservation, now time.Time) error {
	if r == nil || r.Spec.Protocol != Protocol || r.UID == "" || now.IsZero() || r.Status.PreparationCancelledAt == nil || r.Status.PreparationCancelledAt.IsZero() || r.Status.PreparationCancelledAt.After(now) || r.Status.Grant != nil || r.Status.Claim != nil || r.Status.Completion != nil {
		return errors.New("configuration preparation cancellation evidence is incomplete")
	}
	return nil
}
