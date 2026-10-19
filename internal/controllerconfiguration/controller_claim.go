// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"time"
)

// ClaimForController is the one-shot controller-side claim boundary. Authorize
// must check the installed admission/rollout contract and workload policy; a
// scheduling taint alone is insufficient. Success records a claim, never sends
// a controller API request. The returned process-local permit must be checked
// after the journal enters Submitting, immediately before remote submission.
func ClaimForController(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, pod ops.ConfigurationReservationReference, leaseNamespace string, authorize func(context.Context, *ops.ControllerConfigurationReservation) error, now time.Time) (*DispatchPermit, error) {
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	reject := errors.New("configuration controller claim identity or authorization is unavailable")
	if now.IsZero() || reader == nil || writer == nil || expected == nil || expected.UID == "" || expected.ResourceVersion == "" || pod.Name == "" || pod.UID == "" || leaseNamespace == "" || authorize == nil {
		return nil, reject
	}
	var nc cvk.NetworkController
	if err := reader.Get(ctx, client.ObjectKeyFromObject(expected), &nc); err != nil {
		return nil, err
	}
	if nc.UID != expected.UID || nc.ResourceVersion != expected.ResourceVersion || nc.Spec.Type == "" || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil || nc.Status.Configuration.Operation.Phase != "Prepared" {
		return nil, reject
	}
	op := nc.Status.Configuration.Operation
	name, err := ReservationName(string(nc.UID), op.ID)
	if err != nil {
		return nil, err
	}
	var r ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, client.ObjectKey{Namespace: nc.Namespace, Name: name}, &r); err != nil {
		return nil, err
	}
	if r.Spec.Controller.Name != nc.Name || r.Spec.Controller.UID != string(nc.UID) || r.Spec.ControllerGeneration != nc.Generation || r.Spec.ControllerWorkerPod != pod || r.Status.Claim != nil || r.Status.Completion != nil {
		return nil, reject
	}
	validate := func() error {
		if err := ReadIntentBinding(ctx, reader, &r, nc.Spec.Type); err != nil {
			return err
		}
		if err := ReadDeviceWorkerBinding(ctx, reader, &r, observedNow()); err != nil {
			return err
		}
		if err := ReadControllerWorkerBinding(ctx, reader, &r, observedNow()); err != nil {
			return err
		}
		if err := readEmptyConfigurationWorkloads(ctx, reader, &r); err != nil {
			return err
		}
		return ReadGuardedGrant(ctx, reader, &r, leaseNamespace, observedNow())
	}
	if err := validate(); err != nil {
		return nil, err
	}
	if err := authorize(ctx, r.DeepCopy()); err != nil {
		return nil, err
	}
	if err := validate(); err != nil {
		return nil, err
	}
	if err := ClaimOnce(ctx, reader, writer, &r, op, leaseNamespace, observedNow()); err != nil {
		return nil, err
	}
	var claimed ops.ControllerConfigurationReservation
	if err := reader.Get(ctx, client.ObjectKeyFromObject(&r), &claimed); err != nil {
		return nil, err
	}
	if claimed.UID != r.UID || claimed.Status.Claim == nil || claimed.Status.Claim.OperationID != op.ID || claimed.Status.Claim.OperationDigest != r.Spec.OperationDigest {
		return nil, reject
	}
	return &DispatchPermit{state: &dispatchPermitState{reader: reader, reservation: claimed.DeepCopy(), controllerType: nc.Spec.Type, leaseNamespace: leaseNamespace, authorize: authorize}}, nil
}
