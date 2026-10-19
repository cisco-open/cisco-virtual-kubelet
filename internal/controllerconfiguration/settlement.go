// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"github.com/cisco/virtual-kubelet-cisco/internal/configengine/engine"
	"github.com/cisco/virtual-kubelet-cisco/internal/devicecoordination"
	coordination "k8s.io/api/coordination/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// PublishCompletion records verified receipt evidence with a reservation CAS.
// The caller must be the admitted controller worker and use uncached reads.
// Success records completion only; it never releases or acquires a device Lease.
func PublishCompletion(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *ops.ControllerConfigurationReservation, now time.Time) error {
	r, nc, err := settlementObjects(ctx, reader, expected)
	if err != nil {
		return err
	}
	if writer == nil {
		return errors.New("configuration status writer unavailable")
	}
	if r.Status.Completion != nil {
		return ValidateCompletionReceipt(r, nc, now)
	}
	if r.Status.Grant == nil || r.Status.Grant.Phase != "Held" {
		return errors.New("new configuration completion requires a held grant")
	}
	completion, err := CompletionForReceipt(r, nc, now)
	if err != nil {
		return err
	}
	r.Status.Completion = completion
	if err = writer.Status().Update(ctx, r); err != nil {
		return err
	}
	return verifySettlementPersistence(ctx, reader, r)
}

// MarkVerifiedReleased records the device worker's independently checked
// completion. Call only after that worker has safely released its exact Lease
// incarnation (or proved it no longer owns it). This helper does not manipulate
// the Lease and cannot authorize a physical mutation. A crash before this CAS
// leaves the reservation fenced for recovery.
func MarkVerifiedReleased(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *ops.ControllerConfigurationReservation, now time.Time) error {
	r, nc, err := settlementObjects(ctx, reader, expected)
	if err != nil {
		return err
	}
	if writer == nil {
		return errors.New("configuration status writer unavailable")
	}
	if err = ValidateCompletionReceipt(r, nc, now); err != nil {
		return err
	}
	if err := readReleasedConfigurationLease(ctx, reader, r, r.Status.Grant.LeaseNamespace); err != nil {
		return err
	}
	r.Status.Grant.Phase = "Released"
	if err = writer.Status().Update(ctx, r); err != nil {
		return err
	}
	return verifySettlementPersistence(ctx, reader, r)
}

func settlementObjects(ctx context.Context, reader client.Reader, expected *ops.ControllerConfigurationReservation) (*ops.ControllerConfigurationReservation, *cvk.NetworkController, error) {
	if reader == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" {
		return nil, nil, errors.New("configuration settlement requires versioned evidence")
	}
	r := &ops.ControllerConfigurationReservation{}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), r); err != nil {
		return nil, nil, err
	}
	if r.UID != expected.UID || r.ResourceVersion != expected.ResourceVersion || !reflect.DeepEqual(r.Spec, expected.Spec) {
		return nil, nil, errors.New("configuration reservation changed during settlement")
	}
	nc := &cvk.NetworkController{}
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}, nc); err != nil {
		return nil, nil, err
	}
	return r, nc, nil
}
func verifySettlementPersistence(ctx context.Context, reader client.Reader, expected *ops.ControllerConfigurationReservation) error {
	actual := &ops.ControllerConfigurationReservation{}
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), actual); err != nil {
		return err
	}
	if actual.UID != expected.UID || !reflect.DeepEqual(actual.Spec, expected.Spec) || !reflect.DeepEqual(actual.Status, expected.Status) {
		return errors.New("configuration settlement persistence could not be verified")
	}
	return nil
}

// This is an ownership observation, not a Lease deletion or release permit.
func readReleasedConfigurationLease(ctx context.Context, reader client.Reader, r *ops.ControllerConfigurationReservation, namespace string) error {
	if reader == nil || r == nil || r.Status.Grant == nil {
		return errors.New("configuration release evidence unavailable")
	}
	// Observe actual ownership after the device worker releases. Expiration is
	// insufficient: an expired Lease retaining this holder still requires cleanup.
	g := r.Status.Grant
	canonical := engine.LeaseName(devicecoordination.DeviceKey(r.Namespace, r.Spec.Device.Name), devicecoordination.MutationLeaseFamily)
	holder, holderErr := Holder(r.Namespace, r)
	if holderErr != nil || g.Lease.Name != canonical || namespace == "" || g.LeaseNamespace != namespace || g.Holder != holder {
		return errors.New("configuration release lease binding is invalid")
	}
	return readConfigurationLeaseWithoutHolder(ctx, reader, namespace, canonical, holder)
}

// Callers must first bind the canonical lease coordinates and reservation holder
// to either a persisted grant or the exact settled maintenance session.
func readConfigurationLeaseWithoutHolder(ctx context.Context, reader client.Reader, namespace, name, holder string) error {
	if reader == nil || namespace == "" || name == "" || holder == "" {
		return errors.New("configuration lease ownership evidence unavailable")
	}
	var lease coordination.Lease
	if err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: name}, &lease); err != nil {
		if !apierrors.IsNotFound(err) {
			return err
		}
	} else if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity == holder {
		return errors.New("configuration reservation still holds its device lease")
	}
	return nil
}
