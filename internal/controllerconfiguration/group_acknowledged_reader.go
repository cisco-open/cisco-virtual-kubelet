// Copyright 2026 Cisco Systems Inc.
// SPDX-License-Identifier: Apache-2.0
package controllerconfiguration

import (
	"context"
	"errors"
	"strings"
	"time"

	ops "github.com/cisco/virtual-kubelet-cisco/api/ops/v1alpha1"
	cvk "github.com/cisco/virtual-kubelet-cisco/api/v1alpha1"
	"k8s.io/apimachinery/pkg/api/equality"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ReadAcknowledgedGroupReservations resolves the exact journal member set for
// restart recovery. It never reconstructs members from inventory, refreshes a
// claim or grants submission authority. Callers must separately authorize the
// bound worker, verify native evidence and use the existing transition CAS.
func ReadAcknowledgedGroupReservations(ctx context.Context, reader client.Reader, expected *cvk.NetworkController, now time.Time) ([]*ops.ControllerConfigurationReservation, error) {
	reject := errors.New("group recovery requires an unchanged acknowledged journal")
	if reader == nil || expected == nil || expected.ResourceVersion == "" || expected.Status.Configuration == nil || expected.Status.Configuration.Operation == nil || now.IsZero() {
		return nil, reject
	}
	expected = expected.DeepCopy()
	op := expected.Status.Configuration.Operation
	if (op.Phase != "Accepted" && op.Phase != "Unknown" && op.Phase != "Verifying") || ValidateGroupJournalIdentity(op) != nil || !validGroupIdentityToken(op.TaskID) || strings.ContainsAny(op.TaskID, "/\\?#") || op.SubmittedAt == nil || op.SubmittedAt.IsZero() || op.SubmittedAt.After(now) || op.CancellationRequestedAt != nil {
		return nil, reject
	}
	members, err := readJournalGroupReservations(ctx, reader, expected)
	if err != nil {
		return nil, err
	}
	nc, current, err := readGroupSettlementObjects(ctx, reader, expected, members)
	if err != nil {
		return nil, err
	}
	if nc.Spec.Paused || nc.DeletionTimestamp != nil || nc.Generation != op.ControllerGeneration || nc.ResourceVersion != expected.ResourceVersion || nc.Status.Configuration == nil || !equality.Semantic.DeepEqual(nc.Status.Configuration.Operation, op) {
		return nil, reject
	}
	for i := range members {
		if !equality.Semantic.DeepEqual(members[i], current[i]) {
			return nil, reject
		}
	}
	return validateAcknowledgedGroupClaims(nc, current)
}
