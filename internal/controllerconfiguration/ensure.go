// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"slices"
	"time"
)

// EnsureReservation is the manager's idempotent creation boundary. Existing
// evidence is returned unchanged, even when new creation is disabled. Authorize
// is mandatory for creation and must verify installed admission/RBAC, worker
// rollout compatibility and applicable workload policy. It receives a copy.
// Reader must be uncached; writer must use the manager's admitted identity.
// This helper neither registers reconciliation nor changes any Lease or grant.
func EnsureReservation(ctx context.Context, reader client.Reader, writer client.Writer, expected *cvk.NetworkController, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) (*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("configuration reservation identity or creation authority is unavailable")
	if reader == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" {
		return nil, reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return nil, err
	}
	if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return nil, reject
	}
	name, err := ReservationName(string(nc.UID), nc.Status.Configuration.Operation.ID)
	if err != nil {
		return nil, err
	}
	key := client.ObjectKey{Namespace: nc.Namespace, Name: name}
	existing := &ops.ControllerConfigurationReservation{}
	err = reader.Get(ctx, key, existing)
	if err == nil {
		if existing.Spec.Controller.Name != nc.Name || existing.Spec.Controller.UID != string(nc.UID) || !slices.Contains(existing.Finalizers, Finalizer) || len(existing.OwnerReferences) != 0 || ValidateOperationBinding(existing, nc.Status.Configuration.Operation) != nil {
			return nil, reject
		}
		return existing, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, err
	}
	if writer == nil || authorize == nil {
		return nil, reject
	}
	candidate, err := ReadReservationCandidate(ctx, reader, &nc, now)
	if err != nil {
		return nil, err
	}
	if err := authorize(ctx, candidate.DeepCopy()); err != nil {
		return nil, err
	}
	if err := ReadNetworkMutationRole(ctx, reader, candidate); err != nil {
		return nil, err
	}
	// Authorization can involve API reads. Resolve again before create so a
	// changed source, physical declaration or worker cannot reuse the first view.
	current, err := ReadReservationCandidate(ctx, reader, &nc, now)
	if err != nil {
		return nil, err
	}
	if !reflect.DeepEqual(candidate, current) {
		return nil, reject
	}
	if err := writer.Create(ctx, candidate); err != nil {
		// Includes AlreadyExists and lost responses. Do not update, delete or retry
		// under an uncertain result; the next reconciliation reads retained evidence.
		return nil, err
	}
	if candidate.UID == "" {
		return nil, errors.New("configuration reservation create did not return its API identity")
	}
	persisted := &ops.ControllerConfigurationReservation{}
	if err := reader.Get(ctx, key, persisted); err != nil {
		return nil, err
	}
	if persisted.UID != candidate.UID || !reflect.DeepEqual(persisted.Spec, candidate.Spec) || !slices.Contains(persisted.Finalizers, Finalizer) || len(persisted.OwnerReferences) != 0 || persisted.DeletionTimestamp != nil {
		return nil, reject
	}
	return persisted, nil
}
