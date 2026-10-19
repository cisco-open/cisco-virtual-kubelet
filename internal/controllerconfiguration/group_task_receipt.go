// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"strings"
	"time"

	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// RecordTask retains the native acknowledgement after this process consumed a
// valid dispatch permit. It never submits/retries a native request. A missing,
// rejected receipt leaves Submitting for recovery. A lost write response may
// already have persisted Accepted and must be resolved by readback. Copies
// cannot retry this status write or restore dispatch authorization.
func (p *GroupDispatchPermit) RecordTask(ctx context.Context, taskID string, now time.Time) error {
	reject := errors.New("group task receipt requires successful dispatch and unchanged authority")
	if p == nil || p.state == nil || !p.state.authorized.Load() || !validGroupIdentityToken(taskID) || strings.ContainsAny(taskID, "/\\?#") || now.IsZero() {
		return reject
	}
	if p.state.receiptRecorded.Swap(true) {
		return reject
	}
	s := p.state.binding
	if s == nil || s.writer == nil {
		return reject
	}
	started := time.Now()
	observedNow := func() time.Time { return now.Add(time.Since(started)) }
	if _, err := s.validate(ctx, "Submitting", p.state.submittedAt, observedNow()); err != nil {
		return err
	}
	if err := s.authorize(ctx, copyGroupReservations(s.claims)); err != nil {
		return err
	}
	nc, err := s.validate(ctx, "Submitting", p.state.submittedAt, observedNow())
	if err != nil {
		return err
	}
	nc.Status.Configuration.Operation.TaskID = taskID
	nc.Status.Configuration.Operation.Phase = "Accepted"
	op := nc.Status.Configuration.Operation.DeepCopy()
	if err := s.writer.Status().Update(ctx, nc); err != nil {
		return err
	}
	var stored cvk.NetworkController
	if err := s.reader.Get(ctx, client.ObjectKeyFromObject(nc), &stored); err != nil {
		return err
	}
	if stored.UID != nc.UID || stored.Status.Configuration == nil || !equality.Semantic.DeepEqual(stored.Status.Configuration.Operation, op) {
		return reject
	}
	return nil
}
