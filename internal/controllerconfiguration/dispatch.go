// Copyright © 2026 Cisco Systems Inc.
// Licensed under the Apache License, Version 2.0 (the "License");
package controllerconfiguration

import (
	"context"
	"errors"
	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"reflect"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sync/atomic"
	"time"
)

// DispatchPermit exists only in the process that successfully claimed work.
// It is not serializable or reconstructible from persisted claim evidence.
// Copies share consumption state. A failed check also consumes the permit:
// uncertain or changed authority must enter recovery, never retry submission.
type DispatchPermit struct{ state *dispatchPermitState }
type dispatchPermitState struct {
	consumed       atomic.Bool
	reader         client.Reader
	reservation    *ops.ControllerConfigurationReservation
	controllerType cvk.NetworkControllerType
	leaseNamespace string
	authorize      func(context.Context, *ops.ControllerConfigurationReservation) error
}

// Check consumes the permit and corroborates live authority after the journal
// enters Submitting. The caller may issue its single remote request only on nil
// error, without retry; this method itself performs Kubernetes reads only.
func (p *DispatchPermit) Check(ctx context.Context, now time.Time) error {
	reject := errors.New("configuration dispatch permit is unavailable or already consumed")
	if p == nil || p.state == nil || p.state.consumed.Swap(true) {
		return reject
	}
	s := p.state
	if now.IsZero() || s.reader == nil || s.reservation == nil || s.authorize == nil {
		return reject
	}
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	validate := func() error {
		var r ops.ControllerConfigurationReservation
		if err := s.reader.Get(ctx, client.ObjectKeyFromObject(s.reservation), &r); err != nil {
			return err
		}
		if r.UID != s.reservation.UID || r.DeletionTimestamp != nil || !reflect.DeepEqual(r.Spec, s.reservation.Spec) || !reflect.DeepEqual(r.Status.Claim, s.reservation.Status.Claim) || r.Status.Claim == nil || r.Status.Completion != nil {
			return reject
		}
		var nc cvk.NetworkController
		if err := s.reader.Get(ctx, client.ObjectKey{Namespace: r.Namespace, Name: r.Spec.Controller.Name}, &nc); err != nil {
			return err
		}
		if nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
			return reject
		}
		op := nc.Status.Configuration.Operation
		if op.Phase != "Submitting" || op.TaskID != "" || op.SubmittedAt == nil || op.SubmittedAt.Before(&r.Status.Claim.ClaimedAt) || op.SubmittedAt.After(observedNow()) || ValidateOperationBinding(&r, op) != nil {
			return reject
		}
		if err := readIntentAtPhase(ctx, s.reader, &r, s.controllerType, "Submitting"); err != nil {
			return err
		}
		if err := ReadDeviceWorkerBinding(ctx, s.reader, &r, observedNow()); err != nil {
			return err
		}
		if err := ReadControllerWorkerBinding(ctx, s.reader, &r, observedNow()); err != nil {
			return err
		}
		if err := readEmptyConfigurationWorkloads(ctx, s.reader, &r); err != nil {
			return err
		}
		return ReadGuardedGrant(ctx, s.reader, &r, s.leaseNamespace, observedNow())
	}
	if err := validate(); err != nil {
		return err
	}
	if err := s.authorize(ctx, s.reservation.DeepCopy()); err != nil {
		return err
	}
	return validate()
}
