// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RetireSettledReservation removes manager-settled evidence only after the
// controller has cleared or advanced its journal. Call with uncached reads and the admitted
// manager identity. Never use this to cancel preparation or resolve uncertainty.
// A failed/ambiguous write is returned without retry; the next reconciliation
// must read the current object before calling again.
func RetireSettledReservation(ctx context.Context, reader client.Reader, writer client.Writer, expected *ops.ControllerConfigurationReservation, now time.Time) error {
	reject := errors.New("configuration reservation is not ready for retirement")
	if reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || now.IsZero() {
		return reject
	}
	var r ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &r); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if r.UID != expected.UID || r.ResourceVersion != expected.ResourceVersion || !reflect.DeepEqual(r.Spec, expected.Spec) || len(r.OwnerReferences) != 0 {
		return reject
	}
	if err := ValidateManagerSettlement(&r, now); err != nil {
		return err
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}, &nc); err != nil {
		return err
	}
	// A newer operation may need to replace this receipt after retirement. It
	// must not strand settled evidence; this cleanup touches no current Lease,
	// controller journal or receipt. The old operation itself must be gone.
	if string(nc.UID) != r.Spec.Controller.UID || nc.Status.Configuration == nil || (nc.Status.Configuration.Operation != nil && nc.Status.Configuration.Operation.ID == r.Spec.OperationID) {
		return reject
	}
	if r.Status.Claim != nil {
		if err := ValidateCompletionReceipt(&r, &nc, now); err != nil {
			return err
		}
	}
	if slices.Contains(r.Finalizers, Finalizer) {
		r.Finalizers = slices.DeleteFunc(r.Finalizers, func(v string) bool { return v == Finalizer })
		if err := writer.Update(ctx, &r); err != nil {
			return err
		}
	}
	// An earlier DELETE may complete when the finalizer is removed. Otherwise
	// preconditions prevent deleting a replacement or a concurrently changed row.
	err := writer.Delete(ctx, &r, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &r.UID, ResourceVersion: &r.ResourceVersion}})
	if apierrors.IsNotFound(err) {
		return nil
	}
	return err
}
