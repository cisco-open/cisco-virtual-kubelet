// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	coordination "k8s.io/api/coordination/v1"
	"k8s.io/apimachinery/pkg/api/equality"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// GroupSubmissionPermit is created only by successful claims in this process.
// Copies share consumption state. It is not reconstructible from a journal or
// claim list. Runtime callers must explicitly verify group installation.
type GroupSubmissionPermit struct{ state *groupSubmissionState }
type groupSubmissionState struct {
	consumed   atomic.Bool
	reader     client.Reader
	writer     client.StatusClient
	controller *cvk.NetworkController
	claims     []*ops.ControllerConfigurationReservation
	namespace  string
	authorize  func(context.Context, []*ops.ControllerConfigurationReservation) error
}

// BeginSubmissionFor binds a product caller to the controller and operation for
// which this permit was claimed. A permit for another operation cannot dispatch
// the caller's native request. The normal transition repeats live validation.
func (p *GroupSubmissionPermit) BeginSubmissionFor(ctx context.Context, expected *cvk.NetworkController, now time.Time) (*GroupDispatchPermit, error) {
	if p == nil || p.state == nil || p.state.controller == nil || expected == nil || expected.Status.Configuration == nil || p.state.controller.Status.Configuration == nil || client.ObjectKeyFromObject(expected) != client.ObjectKeyFromObject(p.state.controller) || expected.UID != p.state.controller.UID || !equality.Semantic.DeepEqual(expected.Status.Configuration.Operation, p.state.controller.Status.Configuration.Operation) {
		return nil, errors.New("group submission permit belongs to a different operation")
	}
	return p.BeginSubmission(ctx, now)
}

// ClaimGroupForController records all claims before returning a one-use journal
// transition permit. Authorize must establish group schema/admission and worker
// compatibility, fresh intent/physical identity, acknowledged scheduling guards
// and native/Kubernetes workload policy. It is called again before submission.
// This function never issues a controller API request.
func ClaimGroupForController(ctx context.Context, reader client.Reader, writer client.StatusClient, expected *cvk.NetworkController, reservations []*ops.ControllerConfigurationReservation, namespace string, authorize func(context.Context, []*ops.ControllerConfigurationReservation) error, now time.Time) (*GroupSubmissionPermit, error) {
	claimed, err := ClaimGroupOnce(ctx, reader, writer, expected, reservations, namespace, authorize, now)
	if err != nil {
		return nil, err
	}
	return &GroupSubmissionPermit{state: &groupSubmissionState{reader: reader, writer: writer, controller: expected.DeepCopy(), claims: copyGroupReservations(claimed), namespace: namespace, authorize: authorize}}, nil
}

// BeginSubmission consumes the permit even on failure. Only a successful journal
// CAS and exact readback return a final dispatch permit. A timeout/conflict never
// authorizes retry, even if Submitting persisted. Partial claims stay retained.
func (p *GroupSubmissionPermit) BeginSubmission(ctx context.Context, now time.Time) (*GroupDispatchPermit, error) {
	reject := errors.New("group submission permit unavailable or consumed")
	if p == nil || p.state == nil || p.state.consumed.Swap(true) {
		return nil, reject
	}
	s := p.state
	if s.writer == nil || now.IsZero() {
		return nil, reject
	}
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	if _, err := s.validate(ctx, "Prepared", nil, observedNow()); err != nil {
		return nil, err
	}
	if err := s.authorize(ctx, copyGroupReservations(s.claims)); err != nil {
		return nil, err
	}
	nc, err := s.validate(ctx, "Prepared", nil, observedNow())
	if err != nil {
		return nil, err
	}
	at := metav1.NewTime(observedNow().UTC().Truncate(time.Second))
	nc.Status.Configuration.Operation.Phase = "Submitting"
	nc.Status.Configuration.Operation.SubmittedAt = &at
	desiredOperation := nc.Status.Configuration.Operation.DeepCopy()
	if err := s.writer.Status().Update(ctx, nc); err != nil {
		return nil, err
	}
	var persisted cvk.NetworkController
	if err := s.reader.Get(ctx, client.ObjectKeyFromObject(nc), &persisted); err != nil {
		return nil, err
	}
	if persisted.UID != s.controller.UID || persisted.Status.Configuration == nil || !equality.Semantic.DeepEqual(persisted.Status.Configuration.Operation, desiredOperation) {
		return nil, reject
	}
	return &GroupDispatchPermit{state: &groupDispatchState{binding: s, submittedAt: desiredOperation.SubmittedAt.DeepCopy()}}, nil
}

type GroupDispatchPermit struct{ state *groupDispatchState }
type groupDispatchState struct {
	authorized      atomic.Bool
	receiptRecorded atomic.Bool
	consumed        atomic.Bool
	binding         *groupSubmissionState
	submittedAt     *metav1.Time
}

// Check consumes the final permit and repeats all-member authority validation.
// A nil result permits the caller's ONE no-retry request. It makes no API request
// to Catalyst Center itself. Failure retains Submitting for explicit recovery.
func (p *GroupDispatchPermit) Check(ctx context.Context, now time.Time) error {
	reject := errors.New("group dispatch permit unavailable or consumed")
	if p == nil || p.state == nil || p.state.consumed.Swap(true) || p.state.binding == nil {
		return reject
	}
	s := p.state.binding
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	if now.IsZero() {
		return reject
	}
	if _, err := s.validate(ctx, "Submitting", p.state.submittedAt, observedNow()); err != nil {
		return err
	}
	if err := s.authorize(ctx, copyGroupReservations(s.claims)); err != nil {
		return err
	}
	_, err := s.validate(ctx, "Submitting", p.state.submittedAt, observedNow())
	if err == nil {
		p.state.authorized.Store(true)
	}
	return err
}

func (s *groupSubmissionState) validate(ctx context.Context, phase string, submittedAt *metav1.Time, now time.Time) (*cvk.NetworkController, error) {
	reject := errors.New("group submission binding or live authority changed")
	if s.reader == nil || s.controller == nil || s.authorize == nil || now.IsZero() {
		return nil, reject
	}
	var nc cvk.NetworkController
	if err := s.reader.Get(ctx, client.ObjectKeyFromObject(s.controller), &nc); err != nil {
		return nil, err
	}
	if nc.UID != s.controller.UID || nc.Spec.Paused || nc.DeletionTimestamp != nil || nc.Generation != s.controller.Generation || nc.Status.Configuration == nil || nc.Status.Configuration.Operation == nil {
		return nil, reject
	}
	op := nc.Status.Configuration.Operation
	expected := s.controller.Status.Configuration.Operation.DeepCopy()
	expected.Phase = phase
	expected.SubmittedAt = submittedAt
	if !equality.Semantic.DeepEqual(op, expected) || (phase == "Prepared" && nc.ResourceVersion != s.controller.ResourceVersion) {
		return nil, reject
	}
	if submittedAt != nil && (submittedAt.IsZero() || submittedAt.After(now)) {
		return nil, reject
	}
	claims := make([]*ops.ControllerConfigurationReservation, 0, len(s.claims))
	leases := make([]*coordination.Lease, 0, len(s.claims))
	for _, prior := range s.claims {
		var r ops.ControllerConfigurationReservation
		if err := s.reader.Get(ctx, client.ObjectKeyFromObject(prior), &r); err != nil {
			return nil, err
		}
		if r.UID != prior.UID || !reflect.DeepEqual(r.Spec, prior.Spec) || !reflect.DeepEqual(r.Status.Claim, prior.Status.Claim) || !reflect.DeepEqual(r.Status.Grant, prior.Status.Grant) || r.Status.Claim == nil || r.Status.Grant == nil {
			return nil, reject
		}
		claim := r.Status.Claim
		if claim.OperationID != op.ID || claim.OperationDigest != r.Spec.OperationDigest || claim.ClaimedAt.IsZero() || claim.ClaimedAt.After(now) || (submittedAt != nil && submittedAt.Before(&claim.ClaimedAt)) {
			return nil, reject
		}
		var lease coordination.Lease
		if err := s.reader.Get(ctx, client.ObjectKey{Namespace: s.namespace, Name: r.Status.Grant.Lease.Name}, &lease); err != nil {
			return nil, err
		}
		if err := ValidateGrantFreshness(&r, now); err != nil {
			return nil, err
		}
		claims = append(claims, &r)
		leases = append(leases, &lease)
	}
	if err := ValidateGroupLiveGrants(&nc, claims, leases, s.namespace, now); err != nil {
		return nil, err
	}
	return &nc, nil
}
